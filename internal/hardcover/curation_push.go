package hardcover

import (
	"context"
	"errors"
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
)

// curation_push.go — the OUTBOUND half of a per-item curation edit. When the user
// PATCHes a reading_item's status/rating/finish, the endpoint stamps the override
// layer (db.SetReadingItemCuration) and enqueues CurationPushKind; this handler
// mirrors the resulting EFFECTIVE curation onto the user's Hardcover shelf. It is
// the sibling of sync.go's pull (the INBOUND half) and of audiobooks'
// hardcover-push (which only ever mirrors a FINISH). Unlike those, it pushes an
// ARBITRARY chosen status (want/reading/read/paused/dnf) + rating + finish date,
// mapped through the shared StatusID enum — never a hardcoded Reading/Read.
//
// Every write flows through the client's dry-run gate: under dry-run
// UpsertUserBookCuration returns id 0 (logged, not applied), so the whole push is
// a logged no-op AND the echo-suppression stamp is deliberately skipped (nothing
// changed on Hardcover, so there is no echo to suppress).

// CurationPushKind is the catalyst-go-jobs kind for a per-item curation push.
// Owner-scoped; registered + concurrency-capped (shares Hardcover's global rate
// budget) inside main.go's BooksEnabled block.
const CurationPushKind = "hardcover-push-curation"

// CurationPushPayload is the self-contained job payload: the owner + the row key
// (source + external_id/ASIN) whose effective curation to mirror. The handler
// re-reads the row so it always pushes the freshly-committed state (never a stale
// snapshot).
type CurationPushPayload struct {
	Owner      string `json:"owner"`
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
}

// PushService performs a per-item curation push for one user. It borrows the token
// Store (per-user encrypted bearer token, dry-run-gated client) and the DB (row
// read + echo-suppression stamp). Source-agnostic: it mirrors kindle AND audible
// rows, since a curation override can land on either.
type PushService struct {
	DB     *db.DB
	Store  *Store
	Logger *slog.Logger
}

// NewPushService wires the outbound curation push to its dependencies.
func NewPushService(database *db.DB, store *Store, logger *slog.Logger) *PushService {
	return &PushService{DB: database, Store: store, Logger: logger}
}

// DeleteHardcoverRead removes a user_book_read on the owner's Hardcover account
// (the outbound half of deleting a read in the bridge). No-op if the user hasn't
// connected Hardcover. Dry-run-gated. Best-effort: the local delete already
// happened; a Hardcover-side miss is logged, not fatal.
func (s *PushService) DeleteHardcoverRead(ctx context.Context, owner string, readID int64) error {
	if s.Store == nil || readID <= 0 {
		return nil
	}
	client, ok, err := s.Store.ClientForUser(ctx, owner)
	if err != nil {
		return err
	}
	if !ok {
		return nil // Hardcover not connected — nothing to delete remotely
	}
	return client.DeleteUserBookRead(ctx, readID)
}

// PushCuration mirrors reading_items[id]'s EFFECTIVE curation to Hardcover. No-op
// (nil) when Hardcover is not configured/connected, the row is gone, its status is
// unmappable, or no confident match exists. Returns the underlying error on a
// Match / UpsertUserBookCuration / UpsertRead failure (after routing a bad token
// through MarkInvalid so the settings UI prompts a re-paste). This is the
// CurationPushKind handler body.
func (s *PushService) PushCuration(ctx context.Context, p CurationPushPayload) error {
	if s.Store == nil {
		return nil
	}
	client, ok, err := s.Store.ClientForUser(ctx, p.Owner)
	if err != nil {
		s.logWarn(ctx, "hardcover curation-push: client load failed", "user", p.Owner, "err", err)
		return err
	}
	if !ok {
		return nil // user hasn't connected Hardcover — nothing to push
	}

	it, err := s.DB.GetReadingItem(ctx, p.Owner, p.Source, p.ExternalID)
	if err != nil {
		s.logWarn(ctx, "hardcover curation-push: row load failed", "user", p.Owner, "source", p.Source, "externalId", p.ExternalID, "err", err)
		return err // includes pgx.ErrNoRows — the row vanished; the job will retry/expire
	}

	status := it.EffectiveStatus()
	statusID := StatusID(status)
	if statusID == 0 {
		s.logWarn(ctx, "hardcover curation-push: unmappable status — skipped", "user", p.Owner, "externalId", it.ExternalID, "status", status)
		return nil
	}
	format := FormatForSource(it.Source)

	// Resolve the Hardcover book/edition: prefer the cached link (the match ladder
	// already resolved it — never re-fuzz), else run the match once.
	bookID, editionID := int64(0), int64(0)
	if it.HardcoverBookID != nil {
		bookID = *it.HardcoverBookID
	}
	if it.HardcoverEditionID != nil {
		editionID = *it.HardcoverEditionID
	}
	if bookID <= 0 {
		match, merr := client.Match(ctx, MatchInput{
			ASIN:   firstNonEmpty(it.ExternalID, it.AmazonASIN),
			ISBN13: it.ISBN,
			Title:  it.Title,
			Author: it.Authors,
		})
		if merr != nil {
			s.onError(ctx, p.Owner, "match", merr)
			return merr
		}
		if match.BookID <= 0 {
			s.logInfo(ctx, "hardcover curation-push: no confident match — left for review", "user", p.Owner, "externalId", it.ExternalID, "title", it.Title)
			return nil
		}
		bookID, editionID = match.BookID, match.EditionID
	}

	// Dry-run preview: surface WHAT WOULD be pushed and stop before any mutation.
	// The client's graphql() gate would block the writes anyway; doing it here gives
	// a clear per-item preview AND makes explicit that we do NOT stamp the
	// echo-suppression columns (no Hardcover change happened).
	if client.DryRun() {
		s.logInfo(ctx, "hardcover DRYRUN: would push curation",
			"user", p.Owner, "externalId", it.ExternalID, "title", firstNonEmpty(it.Title, it.ExternalID),
			"bookId", bookID, "editionId", editionID, "status", status, "statusId", statusID)
		return nil
	}

	rating := it.EffectiveRating()
	userBookID, err := client.UpsertUserBookCuration(ctx, bookID, editionID, statusID, format, rating)
	if err != nil {
		s.onError(ctx, p.Owner, "upsert user_book", err)
		return err
	}
	if userBookID <= 0 {
		// The gate blocked the write (or Hardcover returned no id): no user_book to
		// attach a read to, nothing changed on Hardcover, so skip the read push AND
		// the echo-suppression stamp. Logged already by the gate.
		return nil
	}

	// Push the finish DATE when the effective curation carries one (a user finish
	// override or an Amazon-finish promotion). The read row is pinned to the matched
	// edition/format so it lands on the right printing.
	if fa := it.EffectiveFinishedAt(); fa != nil {
		finishedAt := *fa
		// Reuse the cached read id so a re-push UPDATES the same read instead of
		// inserting a duplicate (the fix for accumulating reads on Hardcover). 0 on
		// the first push → insert; we then cache the returned id below.
		existingReadID := int64(0)
		if it.HardcoverReadID != nil {
			existingReadID = *it.HardcoverReadID
		}
		readID, rerr := client.UpsertRead(ctx, userBookID, ReadInput{
			FinishedAt:      &finishedAt,
			EditionID:       editionID,
			ReadingFormatID: format,
			UserBookReadID:  existingReadID,
		})
		if rerr != nil {
			s.onError(ctx, p.Owner, "upsert user_book_read", rerr)
			return rerr
		}
		if readID > 0 && readID != existingReadID {
			if serr := s.DB.SetReadingItemPushedReadID(ctx, p.Owner, it.Source, it.ExternalID, readID); serr != nil {
				s.logWarn(ctx, "hardcover curation-push: cache read id failed", "user", p.Owner, "externalId", it.ExternalID, "err", serr)
			}
		}
	}

	// Echo-suppression stamp: record that WE last pushed this status at now(), so
	// the pull's LWW branch does not re-adopt our own write as a remote edit.
	if serr := s.DB.SetReadingItemPushed(ctx, p.Owner, it.Source, it.ExternalID, status); serr != nil {
		s.logWarn(ctx, "hardcover curation-push: echo-suppression stamp failed", "user", p.Owner, "externalId", it.ExternalID, "err", serr)
		// Non-fatal: the push already landed; a missed stamp only risks re-adopting
		// our echo on the next pull, which the LWW timestamp guard still bounds.
	}

	s.logInfo(ctx, "hardcover: pushed curation", "user", p.Owner, "externalId", it.ExternalID, "bookId", bookID, "status", status)
	return nil
}

// onError logs a push-stage failure and, on a bad token, flips the stored key
// status so the settings UI prompts a re-paste (mirrors sync.go's onError).
func (s *PushService) onError(ctx context.Context, owner, stage string, err error) {
	s.logWarn(ctx, "hardcover curation-push failed", "user", owner, "stage", stage, "err", err)
	if errors.Is(err, ErrBadToken) && s.Store != nil {
		if merr := s.Store.MarkInvalid(ctx, owner); merr != nil {
			s.logWarn(ctx, "hardcover curation-push: mark-invalid failed", "user", owner, "err", merr)
		}
	}
}

func (s *PushService) logInfo(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Info(msg, args...)
	}
}

func (s *PushService) logWarn(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Warn(msg, args...)
	}
}
