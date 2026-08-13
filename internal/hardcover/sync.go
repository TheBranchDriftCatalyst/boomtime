package hardcover

import (
	"context"
	"errors"
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

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
		s.logWarn("hardcover pull: client load failed", "user", owner, "err", err)
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
		n, uerr := s.DB.UpdateHardcoverLinkFromPull(ctx, owner, db.HardcoverUserBookLink{
			BookID:          int64(b.BookID),
			Status:          StatusString(b.StatusID),
			RemoteUpdatedAt: b.UpdatedAt,
		})
		if uerr != nil {
			// A single reconcile failure shouldn't abort the whole sweep.
			s.logWarn("hardcover pull: reconcile failed", "user", owner, "bookId", b.BookID, "err", uerr)
			continue
		}
		if n > 0 {
			res.Linked++
			continue
		}
		res.Unlinked++
		// Inbound-origin: on the shelf but not tracked locally. Logged only —
		// creating a reading_item from a pull is a documented follow-up.
		s.logInfo("hardcover pull: shelf book has no local reading_item (follow-up: inbound-origin create)",
			"user", owner, "bookId", b.BookID, "title", b.Title, "status", StatusString(b.StatusID))
	}

	s.logInfo("hardcover pull: complete",
		"user", owner, "fetched", res.Fetched, "linked", res.Linked, "unlinked", res.Unlinked)
	return res, nil
}

// onError logs a pull failure and, on a bad token, flips the stored key status so
// the settings UI prompts a re-paste (the Jan-1 reset makes this routine).
func (s *SyncService) onError(ctx context.Context, owner, stage string, err error) {
	s.logWarn("hardcover pull failed", "user", owner, "stage", stage, "err", err)
	if errors.Is(err, ErrBadToken) && s.Store != nil {
		if merr := s.Store.MarkInvalid(ctx, owner); merr != nil {
			s.logWarn("hardcover pull: mark-invalid failed", "user", owner, "err", merr)
		}
	}
}

func (s *SyncService) logInfo(msg string, args ...any) {
	if s.Logger != nil {
		s.Logger.Info(msg, args...)
	}
}

func (s *SyncService) logWarn(msg string, args ...any) {
	if s.Logger != nil {
		s.Logger.Warn(msg, args...)
	}
}
