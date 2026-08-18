package hardcover

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logctx"
)

// hardcoverActivitySource tags reading_activity buckets fed by the Hardcover
// pull (its own source string so the reading-domain "seconds" measure can group
// Hardcover reading time apart from audible/kindle).
const hardcoverActivitySource = "hardcover"

// sync.go — the INBOUND sync service that drives the pull end-to-end: load the
// user's client, resolve their Hardcover id (Me), sweep the shelf (UserBooks),
// and reconcile each entry onto the matching reading_item's minimal linkage
// (UpdateHardcoverLinkFromPull). It creates NO local shelf mirror — books on
// Hardcover with no local reading_item are only LOGGED (inbound-origin creation
// is a documented follow-up, gaka-books).

// PullJobKind is the catalyst-go-jobs kind for the per-user inbound Hardcover
// sync. Owner-scoped (needs the user's token); registered + concurrency-capped in
// main.go only inside the BooksEnabled block.
const PullJobKind = "hardcover-pull"

// SyncService performs the inbound Hardcover pull + reconcile for one user. It
// owns no auth — it borrows the token Store (per-user encrypted bearer token) and
// the DB (linkage reconcile).
type SyncService struct {
	DB     *db.DB
	Store  *Store
	Logger *slog.Logger
}

// NewSyncService wires the inbound sync to its dependencies.
func NewSyncService(database *db.DB, store *Store, logger *slog.Logger) *SyncService {
	return &SyncService{DB: database, Store: store, Logger: logger}
}

// PullResult reports what one SyncHardcoverPull run saw: how many shelf entries
// were fetched, how many reconciled onto a local reading_item (Linked), and how
// many had no local row yet (Unlinked — the follow-up creation candidates). Shelf
// is the in-memory index the outbound push can consult (HasRead) to skip an
// already-finished book.
type PullResult struct {
	Fetched  int
	Linked   int
	Unlinked int
	// Created is how many shelf-only books were materialized as first-class
	// source='hardcover' reading_items this run (the inbound-origin ingest). A row
	// re-upserted on a later pull is NOT counted (it wasn't newly created).
	Created int
	// Listed is how many books had Hardcover list memberships attached this run.
	Listed int
	// Pushed is how many diverged rows were mirrored OUT to Hardcover this run (the
	// outbound half of the two-way sync). 0 under dry-run.
	Pushed int
	Shelf  *Shelf
}

// SyncHardcoverPull runs the inbound sync for owner. It is a no-op (zero result,
// nil error) when the user has not connected Hardcover. On a bad token it flips
// the stored key status to invalid (mirroring the push) so the UI prompts a
// re-paste. Reconcile is minimal-linkage only — no book details are stored.
func (s *SyncService) SyncHardcoverPull(ctx context.Context, owner string) (PullResult, error) {
	var res PullResult
	if s.Store == nil {
		return res, nil
	}
	client, ok, err := s.Store.ClientForUser(ctx, owner)
	if err != nil {
		s.logWarn(ctx, "hardcover pull: client load failed", "user", owner, "err", err)
		return res, err
	}
	if !ok {
		return res, nil // user hasn't connected Hardcover — nothing to pull
	}

	userID, err := client.Me(ctx)
	if err != nil {
		s.onError(ctx, owner, "me", err)
		return res, err
	}

	books, err := client.UserBooks(ctx, userID)
	if err != nil {
		s.onError(ctx, owner, "user_books", err)
		return res, err
	}
	res.Fetched = len(books)
	res.Shelf = BuildShelf(books)

	for _, b := range books {
		// Honor cancellation between per-book reconciles.
		if err := ctx.Err(); err != nil {
			return res, err
		}
		// Mirror the FULL shelf entry (ALL statuses) into the local candidate store
		// (migration 00074) — the pool the sweep's LOCAL shelf-match rung scores
		// unmatched rows against. Best-effort: a mirror-write miss must not fail the
		// pull (the linkage reconcile below is the primary job), and the next pull
		// re-upserts it. Kept ALONGSIDE the by-book_id reconcile, not in place of it.
		updated := b.UpdatedAt
		if serr := s.DB.UpsertHardcoverShelfEntry(ctx, owner, db.ShelfEntry{
			BookID: int64(b.BookID),
			Title:  b.Title,
			Author: b.Author,
			Slug:   b.Slug,
			Status: StatusString(b.StatusID),
		}, &updated); serr != nil {
			s.logWarn(ctx, "hardcover pull: shelf mirror upsert failed", "user", owner, "bookId", b.BookID, "err", serr)
		}
		// Ingest EVERY Hardcover read as a discrete reading_event (migration 00078) —
		// the authoritative multiple-reads history. Idempotent (upsert by the stable
		// user_book_reads id), best-effort. reading_items keeps the latest finish; the
		// events carry the full history a book can be read more than once.
		s.ingestHardcoverReads(ctx, owner, b)
		n, uerr := s.DB.UpdateHardcoverLinkFromPull(ctx, owner, db.HardcoverUserBookLink{
			BookID:          int64(b.BookID),
			Status:          StatusString(b.StatusID),
			Slug:            b.Slug,
			RemoteUpdatedAt: b.UpdatedAt,
			Rating:          b.Rating,
			FinishedAt:      latestFinishedAt(b.Reads),
		})
		if uerr != nil {
			// A single reconcile failure shouldn't abort the whole sweep.
			s.logWarn(ctx, "hardcover pull: reconcile failed", "user", owner, "bookId", b.BookID, "err", uerr)
			continue
		}
		if n > 0 {
			res.Linked++
			continue
		}
		res.Unlinked++
		// Inbound-origin: on the shelf but not tracked locally yet. The ingest pass
		// below materializes it as a source='hardcover' reading_item (unless a
		// Kindle/Audible match already covers this book_id).
	}

	// Inbound-origin ingest (AFTER the shelf mirror + matched-row reconcile): turn
	// every shelf book that no Kindle/Audible reading_item already represents into a
	// first-class source='hardcover' library row, so the fused library includes the
	// physical/library books the user only shelved on Hardcover. Idempotent
	// (owner+source+external_id upsert) and best-effort — a create miss must not
	// fail the pull.
	if created, ierr := s.ingestShelfOnlyBooks(ctx, owner, books); ierr != nil {
		s.logWarn(ctx, "hardcover pull: shelf-only ingest failed", "user", owner, "err", ierr)
	} else {
		res.Created = created
	}

	// Attach Hardcover LIST memberships (Guilty Pleasures, Owned, …) as a property
	// on each book's reading_items (migration 00077) — best-effort, read-only.
	if listed, lerr := s.attachListMemberships(ctx, owner, client, userID); lerr != nil {
		s.logWarn(ctx, "hardcover pull: list attach failed", "user", owner, "err", lerr)
	} else {
		res.Listed = listed
	}

	// Feed real Hardcover reading TIME + read dates into the activity series. Each
	// bucket is (owner, source='hardcover', day) with the summed progress_seconds
	// of every read that finished / progressed that day. Best-effort: a bucket
	// upsert failure must not fail the whole pull (the linkage reconcile above is
	// the primary job).
	s.upsertReadActivity(ctx, owner, books)

	// OUTBOUND half — the two-way sync (gaka-books). Now that the LWW pull above has
	// adopted any Hardcover-newer edits, whatever is STILL diverged is where boomtime
	// is newer → push those out (status/rating), batched. Reuses the same client
	// (its rate limiter), and is dry-run-gated. Best-effort: a push miss never fails
	// the (primary, inbound) pull.
	if pushed, perr := s.PushDivergedToHardcover(ctx, owner, client); perr != nil {
		s.logWarn(ctx, "hardcover sync: outbound push failed", "user", owner, "err", perr)
	} else {
		res.Pushed = pushed
	}

	s.logInfo(ctx, "hardcover sync: complete",
		"user", owner, "fetched", res.Fetched, "linked", res.Linked,
		"unlinked", res.Unlinked, "created", res.Created, "listed", res.Listed,
		"pushed", res.Pushed)
	return res, nil
}

// ingestHardcoverReads upserts every read of a Hardcover shelf book as a discrete
// reading_event (origin='hardcover', keyed by the stable user_book_reads id → a
// re-pull refreshes, never duplicates). Best-effort per read. A shelf entry with no
// reads (want/unread) writes nothing.
func (s *SyncService) ingestHardcoverReads(ctx context.Context, owner string, b UserBook) {
	bookID := int64(b.BookID)
	for _, r := range b.Reads {
		if err := s.DB.UpsertReadingEvent(ctx, db.ReadingEvent{
			Owner:           owner,
			HardcoverBookID: &bookID,
			Origin:          db.ReadingEventOriginHardcover,
			ExternalReadID:  strconv.Itoa(r.ID),
			StartedAt:       r.StartedAt,
			FinishedAt:      r.FinishedAt,
			ProgressPages:   r.ProgressPages,
			ProgressSeconds: r.ProgressSeconds,
		}); err != nil {
			s.logWarn(ctx, "hardcover pull: read event upsert failed", "user", owner, "bookId", b.BookID, "readId", r.ID, "err", err)
		}
	}
}

// bulkPushChunk bounds one batched insert_user_book request. 50 aliased mutations
// per request keeps the operation well under Hasura's node/complexity limits while
// collapsing ~1000 books into ~20 rate-limited requests instead of 1000.
const bulkPushChunk = 50

// PushDivergedToHardcover mirrors every diverged matched row's effective status +
// rating OUT to Hardcover in batched requests, then advances the local mirror
// (hardcover_status) + echo-suppression stamp for each row that wrote. This is the
// bulk outbound half of the two-way sync (gaka-books); the caller runs it AFTER the
// LWW pull so only boomtime-newer rows remain diverged. Dry-run-gated (the client's
// graphql gate blocks the batch; every result stays UserBookID 0 → no stamp).
// Returns how many rows were successfully pushed. Best-effort per chunk.
func (s *SyncService) PushDivergedToHardcover(ctx context.Context, owner string, client *Client) (int, error) {
	if s.DB == nil || client == nil {
		return 0, nil
	}
	diverged, err := s.DB.ListDivergedHardcoverItems(ctx, owner)
	if err != nil {
		return 0, err
	}
	if len(diverged) == 0 {
		return 0, nil
	}

	pushed := 0
	for start := 0; start < len(diverged); start += bulkPushChunk {
		if err := ctx.Err(); err != nil {
			return pushed, err
		}
		end := start + bulkPushChunk
		if end > len(diverged) {
			end = len(diverged)
		}
		batch := diverged[start:end]

		items := make([]BulkUserBookInput, 0, len(batch))
		for _, it := range batch {
			statusID := StatusID(it.EffectiveStatus)
			if statusID == 0 {
				continue // unmappable status — skip (never guess-push)
			}
			items = append(items, BulkUserBookInput{
				Source: it.Source, ExternalID: it.ExternalID, Status: it.EffectiveStatus,
				BookID: it.HardcoverBookID, EditionID: it.HardcoverEditID,
				StatusID: statusID, Rating: it.Rating,
			})
		}
		if len(items) == 0 {
			continue
		}

		results, berr := client.BulkUpsertUserBooks(ctx, items)
		if berr != nil {
			s.onError(ctx, owner, "bulk upsert user_book", berr)
			return pushed, berr // a transport/token error affects the whole batch — stop
		}
		for _, r := range results {
			if r.Err != "" {
				s.logWarn(ctx, "hardcover sync: per-book push error", "user", owner,
					"externalId", r.Input.ExternalID, "err", r.Err)
				continue
			}
			if r.UserBookID <= 0 {
				continue // dry-run (blocked) or no id returned — nothing landed, no stamp
			}
			// Advance the mirror + echo stamp so this row reads as synced and the next
			// pull doesn't re-adopt our own write.
			if serr := s.DB.SetReadingItemPushed(ctx, owner, r.Input.Source, r.Input.ExternalID, r.Input.Status); serr != nil {
				s.logWarn(ctx, "hardcover sync: mirror stamp failed", "user", owner, "externalId", r.Input.ExternalID, "err", serr)
			}
			pushed++
		}
	}
	if pushed > 0 {
		s.logInfo(ctx, "hardcover sync: pushed diverged rows", "user", owner, "count", pushed, "of", len(diverged))
	}
	return pushed, nil
}

// attachListMemberships pulls the user's Hardcover lists and writes each book's
// list-name array onto its reading_items (all editions of the Work, keyed by
// hardcover_book_id). Returns how many books got a non-empty list set. Read-only
// against Hardcover; best-effort per book.
func (s *SyncService) attachListMemberships(ctx context.Context, owner string, client *Client, userID int) (int, error) {
	lists, err := client.UserLists(ctx, userID)
	if err != nil {
		return 0, err
	}
	membership := listMembershipByBook(lists)
	listed := 0
	for bookID, names := range membership {
		if err := ctx.Err(); err != nil {
			return listed, err
		}
		if werr := s.DB.SetReadingItemListsForBook(ctx, owner, bookID, marshalLists(names)); werr != nil {
			s.logWarn(ctx, "hardcover pull: set lists failed", "user", owner, "bookId", bookID, "err", werr)
			continue
		}
		listed++
	}
	return listed, nil
}

// ingestShelfOnlyBooks materializes each shelf book that no Kindle/Audible
// reading_item already represents as a first-class source='hardcover' library row
// — the inbound-origin ingest. It loads the owner's externally-matched book_ids
// once (ListOwnerHardcoverLinkedBookIDs) and skips any shelf book already covered
// by a real ebook/audiobook purchase, so a book the user owns AND shelved shows a
// single fused row, not a duplicate. Everything else is upserted keyed by
// owner+source('hardcover')+external_id(the hardcover_book_id), so a re-pull
// refreshes the row's display/status without ever duplicating it. The Hardcover
// linkage (book id + edition + slug + matched_at) is stamped on because the row is
// inherently Hardcover-origin. Returns how many rows were NEWLY created.
//
// KNOWN follow-up: if a shelf-only book LATER gains a matched Kindle/Audible row,
// this stops re-upserting the hardcover row (it enters the skip set) but does NOT
// delete the already-created one — both rows coexist until a future de-dup pass.
func (s *SyncService) ingestShelfOnlyBooks(ctx context.Context, owner string, books []UserBook) (int, error) {
	if s.DB == nil {
		return 0, nil
	}
	linked, err := s.DB.ListOwnerHardcoverLinkedBookIDs(ctx, owner)
	if err != nil {
		return 0, err
	}
	var created int
	for _, b := range books {
		if err := ctx.Err(); err != nil {
			return created, err
		}
		if b.BookID <= 0 {
			continue // no stable identity → can't key a reading_item
		}
		if linked[int64(b.BookID)] {
			continue // a Kindle/Audible reading_item already represents this book
		}
		externalID := strconv.Itoa(b.BookID)
		newlyCreated, cerr := s.upsertHardcoverSourceRow(ctx, owner, externalID, b)
		if cerr != nil {
			// One bad row must not abort the whole ingest.
			s.logWarn(ctx, "hardcover pull: shelf-only row upsert failed",
				"user", owner, "bookId", b.BookID, "err", cerr)
			continue
		}
		if newlyCreated {
			created++
			s.logInfo(ctx, "hardcover pull: ingested shelf-only book as source=hardcover",
				"user", owner, "bookId", b.BookID, "title", b.Title, "status", StatusString(b.StatusID))
		}
	}
	return created, nil
}

// upsertHardcoverSourceRow writes (or refreshes) the source='hardcover' reading_item
// for one shelf book and stamps its Hardcover linkage. It reports whether the row
// was NEWLY created (false = an existing row was refreshed) by probing existence
// before the upsert. The derived layer (status/finished/rating/dates) is filled
// from the shelf entry; the LWW override reconcile on later pulls keeps curation
// honest.
func (s *SyncService) upsertHardcoverSourceRow(ctx context.Context, owner, externalID string, b UserBook) (bool, error) {
	_, existErr := s.DB.GetReadingItem(ctx, owner, "hardcover", externalID)
	newlyCreated := errors.Is(existErr, pgx.ErrNoRows)
	if existErr != nil && !newlyCreated {
		return false, existErr
	}

	if err := s.DB.UpsertReadingItem(ctx, db.ReadingItem{
		Owner:      owner,
		Source:     "hardcover",
		ExternalID: externalID,
		Title:      b.Title,
		Authors:    b.Author,
		CoverURL:   b.CoverURL,
		Status:     StatusString(b.StatusID),
		Finished:   int64(b.StatusID) == StatusRead,
		StartedAt:  earliestStartedAt(b.Reads),
		FinishedAt: latestFinishedAt(b.Reads),
		Rating:     b.Rating,
	}); err != nil {
		return false, err
	}

	// Stamp the Hardcover linkage — the row is inherently Hardcover-origin, so it is
	// matched by construction (book id + edition + slug + matched_at=now). editionId
	// 0 is COALESCE-guarded away inside the helper.
	if err := s.DB.SetReadingItemHardcoverLink(ctx, owner, "hardcover", externalID,
		int64(b.BookID), int64(b.EditionID), "hardcover", b.Slug); err != nil {
		return false, err
	}
	return newlyCreated, nil
}

// earliestStartedAt returns the earliest started_at across a shelf entry's reads
// (nil when none carries one) — the library "started reading" date for a
// materialized source='hardcover' row. Symmetric with latestFinishedAt.
func earliestStartedAt(reads []UserBookRead) *time.Time {
	var earliest *time.Time
	for _, r := range reads {
		if r.StartedAt == nil {
			continue
		}
		if earliest == nil || r.StartedAt.Before(*earliest) {
			earliest = r.StartedAt
		}
	}
	return earliest
}

// upsertReadActivity writes one reading_activity bucket per (day → summed
// listening seconds) derived from the shelf's user_book_reads. Idempotent: a
// re-pull recomputes each day's total from the full shelf and overwrites the
// bucket (never doubles). Best-effort per bucket.
func (s *SyncService) upsertReadActivity(ctx context.Context, owner string, books []UserBook) {
	if s.DB == nil {
		return
	}
	for day, secs := range aggregateReadActivity(books) {
		if err := s.DB.UpsertReadingActivity(ctx, db.ReadingActivity{
			Owner:            owner,
			Source:           hardcoverActivitySource,
			Granularity:      "day",
			BucketDate:       day,
			ListeningSeconds: secs,
		}); err != nil {
			s.logWarn(ctx, "hardcover pull: reading_activity upsert failed",
				"user", owner, "day", day.Format("2006-01-02"), "err", err)
		}
	}
}

// aggregateReadActivity buckets the shelf's reads by UTC day, summing
// progress_seconds. A read is bucketed when it carries progress_seconds>0 OR a
// finished_at (per gaka-books B); its day is finished_at when present, else
// started_at. Reads with neither a date nor time are skipped. Pure + deterministic
// so it is unit-testable against a fixture without a live client or DB.
func aggregateReadActivity(books []UserBook) map[time.Time]int64 {
	buckets := map[time.Time]int64{}
	for _, b := range books {
		for _, rd := range b.Reads {
			var secs int64
			if rd.ProgressSeconds != nil && *rd.ProgressSeconds > 0 {
				secs = int64(*rd.ProgressSeconds)
			}
			if secs == 0 && rd.FinishedAt == nil {
				continue // no reading time and not a finish → nothing to record
			}
			day := rd.FinishedAt
			if day == nil {
				day = rd.StartedAt
			}
			if day == nil {
				continue // can't place a bucket without a date
			}
			d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
			buckets[d] += secs
		}
	}
	return buckets
}

// latestFinishedAt returns the most recent finished_at across a shelf entry's
// reads (nil when none is finished) — the remote finish DATE the LWW branch adopts
// into finished_at_override when Hardcover is the newer curation writer.
func latestFinishedAt(reads []UserBookRead) *time.Time {
	var latest *time.Time
	for _, r := range reads {
		if r.FinishedAt == nil {
			continue
		}
		if latest == nil || r.FinishedAt.After(*latest) {
			latest = r.FinishedAt
		}
	}
	return latest
}

// onError logs a pull failure and, on a bad token, flips the stored key status so
// the settings UI prompts a re-paste (the Jan-1 reset makes this routine).
func (s *SyncService) onError(ctx context.Context, owner, stage string, err error) {
	s.logWarn(ctx, "hardcover pull failed", "user", owner, "stage", stage, "err", err)
	if errors.Is(err, ErrBadToken) && s.Store != nil {
		if merr := s.Store.MarkInvalid(ctx, owner); merr != nil {
			s.logWarn(ctx, "hardcover pull: mark-invalid failed", "user", owner, "err", merr)
		}
	}
}

// logInfo/logWarn resolve the job-scoped logger from ctx (logctx.FromContext),
// falling back to s.Logger off a job. Threading ctx means every handler line
// inherits the running job's job_id/kind/owner so the Admin viewer can filter to
// one job's run (gaka-f0is).
func (s *SyncService) logInfo(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Info(msg, args...)
	}
}

func (s *SyncService) logWarn(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Warn(msg, args...)
	}
}
