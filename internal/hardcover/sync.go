package hardcover

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
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
	Shelf    *Shelf
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
		n, uerr := s.DB.UpdateHardcoverLinkFromPull(ctx, owner, db.HardcoverUserBookLink{
			BookID:          int64(b.BookID),
			Status:          StatusString(b.StatusID),
			RemoteUpdatedAt: b.UpdatedAt,
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
		// Inbound-origin: on the shelf but not tracked locally. Logged only —
		// creating a reading_item from a pull is a documented follow-up.
		s.logInfo(ctx, "hardcover pull: shelf book has no local reading_item (follow-up: inbound-origin create)",
			"user", owner, "bookId", b.BookID, "title", b.Title, "status", StatusString(b.StatusID))
	}

	// Feed real Hardcover reading TIME + read dates into the activity series. Each
	// bucket is (owner, source='hardcover', day) with the summed progress_seconds
	// of every read that finished / progressed that day. Best-effort: a bucket
	// upsert failure must not fail the whole pull (the linkage reconcile above is
	// the primary job).
	s.upsertReadActivity(ctx, owner, books)

	s.logInfo(ctx, "hardcover pull: complete",
		"user", owner, "fetched", res.Fetched, "linked", res.Linked, "unlinked", res.Unlinked)
	return res, nil
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
