package identity

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains/books"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/labstack/echo/v5"
)

// kindle_connect.go — the catalyst-books (Kindle) ingest triggers, the ebook
// mirror of the Audible triggers in amazon_connect.go. ONE Amazon device
// credential feeds both Kindle + Audible; Hardcover resolves an ASIN → metadata +
// linkage. All routes are BooksEnabled-gated (registered in routes.go).
//
//	POST /api/v1/kindle/sync     → { synced, source } (owner-scoped, inline)
//	POST /api/v1/kindle/backfill → 202 { enqueued, jobId } (owner-scoped job)
//	POST /api/v1/kindle/insights → { datesBackfilled, source } (owner-scoped, inline)

// SyncKindle triggers a Kindle library sync into the siloed reading_items table
// and returns how many items were synced. Owner-scoped + inline (mirrors
// SyncAudible): the Kindle sweep is a single whispersync pull, not a paginated
// crawl, so it runs in-request rather than on the worker.
func (h *Handler) SyncKindle(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	svc := books.New(h.DB, amazon.NewStore(h.DB), h.Logger).
		SetHardcover(hardcover.NewStore(h.DB))
	n, err := svc.SyncUser(c.Request().Context(), owner)
	if err != nil {
		// Surface the Amazon-side error so a signing/format mismatch is debuggable
		// from the UI, exactly like the connect + audible-sync flows.
		return apihelpers.RespondErr(c, apierr.BadRequest(err.Error()))
	}
	h.Logger.Info("kindle synced", "user", owner, "items", n)
	return c.JSON(http.StatusOK, map[string]any{"synced": n, "source": "kindle"})
}

// SyncKindleInsights triggers the Kindle Reading-Insights ingest for the caller:
// fetch the reading history (finish DATES + streaks), store the raw snapshot, and
// backfill finished_at onto the user's existing kindle reading_items. Owner-scoped
// + inline (mirrors SyncKindle): a single GET, not a paginated crawl. Returns how
// many rows had a finish date newly backfilled. Run AFTER a library sync so the
// rows it dates already exist.
func (h *Handler) SyncKindleInsights(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	svc := books.New(h.DB, amazon.NewStore(h.DB), h.Logger).
		SetHardcover(hardcover.NewStore(h.DB))
	n, err := svc.SyncInsights(c.Request().Context(), owner)
	if err != nil {
		// Surface the Amazon-side error so a cookie/format mismatch is debuggable
		// from the UI, exactly like the connect + kindle-sync flows.
		return apihelpers.RespondErr(c, apierr.BadRequest(err.Error()))
	}
	h.Logger.Info("kindle insights synced", "user", owner, "datesBackfilled", n)
	return c.JSON(http.StatusOK, map[string]any{"datesBackfilled": n, "source": "kindle"})
}

// BackfillKindle enqueues the one-shot Kindle backfill for the caller. It runs on
// the jobs worker (owner-scoped payload) and returns the enqueued job id
// immediately. Idempotent to enqueue: the backfill upserts, so a duplicate run is
// harmless. Mirrors BackfillAudible.
func (h *Handler) BackfillKindle(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	// Confirm the user actually has an Amazon credential before enqueueing, so the
	// UI gets an immediate, clear error instead of a job that fails later.
	if _, lerr := amazon.NewStore(h.DB).Load(c.Request().Context(), owner); lerr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("connect Amazon before running a backfill"))
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), books.KindleBackfillKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "kindle backfill enqueue failed", eerr)
	}
	h.Logger.Info("kindle backfill enqueued", "user", owner, "jobId", id)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true, "jobId": id})
}
