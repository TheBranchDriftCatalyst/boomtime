package books

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains/bookspipeline"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/labstack/echo/v5"
)

// books_sync_all.go — the ONE orchestrator trigger that consolidates the whole
// reading-sync pipeline (Audible ingest → Kindle ingest → Hardcover match →
// Hardcover pull) behind a single enqueue, instead of the UI firing four
// separate kinds in order. BooksEnabled-gated (registered in routes.go).
//
//	POST /api/v1/books/sync-all → 202 { enqueued, jobId } (owner-scoped job)

// SyncAllBooks enqueues the consolidated books-sync-all orchestrator for the
// caller. It runs on the jobs worker (owner-scoped payload): the worker chains
// the four stages in dependency order for this user and returns the enqueued job
// id immediately rather than blocking on the full pipeline. BooksEnabled-gated.
// Idempotent to enqueue — every constituent stage is itself re-runnable, so a
// duplicate run is harmless. Mirrors BackfillKindle / PullHardcover.
func (h *Handler) SyncAllBooks(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	// Confirm the user actually has an Amazon credential before enqueueing, so the
	// UI gets an immediate, clear error instead of a job that no-ops later. (The
	// pipeline's ingest stages need it; Hardcover match/pull are no-ops without a
	// Hardcover token, which is fine — the ingests still run.)
	if _, lerr := amazon.NewStore(h.DB).Load(c.Request().Context(), owner); lerr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("connect Amazon before running a full sync"))
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), bookspipeline.BooksSyncAllKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "books sync-all enqueue failed", eerr)
	}
	h.Logger.Info("books sync-all enqueued", "user", owner, "jobId", id)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true, "jobId": id})
}
