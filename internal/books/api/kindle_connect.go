package api

import (
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/ingest/kindle"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/labstack/echo/v5"
)

// kindle_connect.go — the catalyst-books (Kindle) ingest triggers, the ebook
// mirror of the Audible triggers in amazon_connect.go. ONE Amazon device
// credential feeds both Kindle + Audible; Hardcover resolves an ASIN → metadata +
// linkage. All routes are BooksEnabled-gated (registered in routes.go).
//
//	POST /api/v1/kindle/sync      → { synced, source } (owner-scoped, inline)
//	POST /api/v1/kindle/backfill  → 202 { enqueued, jobId } (owner-scoped job)
//	POST /api/v1/kindle/insights  → { datesBackfilled, source } (owner-scoped, inline)
//	POST /api/v1/kindle/reconcile → 202 { enqueued, jobId } (owner-scoped job)

// kindleInsightsResponse is POST /api/v1/kindle/insights: how many existing
// kindle rows had a finish DATE newly backfilled from Reading Insights.
type kindleInsightsResponse struct {
	DatesBackfilled int    `json:"datesBackfilled"`
	Source          string `json:"source"`
}

// SyncKindle triggers a Kindle library sync into the siloed reading_items table
// and returns how many items were synced. Owner-scoped + inline (mirrors
// SyncAudible): the Kindle sweep is a single whispersync pull, not a paginated
// crawl, so it runs in-request rather than on the worker.
func (h *Handler) SyncKindle(c *echo.Context) (ingestSyncResponse, error) {
	var out ingestSyncResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	svc := kindle.New(h.DB, amazon.NewStore(h.DB), h.Logger).
		SetHardcover(hardcover.NewStore(h.DB))
	n, err := svc.SyncUser(c.Request().Context(), owner)
	if err != nil {
		// Surface the Amazon-side error so a signing/format mismatch is debuggable
		// from the UI, exactly like the connect + audible-sync flows.
		return out, apierr.BadRequest(err.Error())
	}
	h.Logger.Info("kindle synced", "user", owner, "items", n)
	return ingestSyncResponse{Synced: n, Source: "kindle"}, nil
}

// SyncKindleInsights triggers the Kindle Reading-Insights ingest for the caller:
// fetch the reading history (finish DATES + streaks), store the raw snapshot, and
// backfill finished_at onto the user's existing kindle reading_items. Owner-scoped
// + inline (mirrors SyncKindle): a single GET, not a paginated crawl. Returns how
// many rows had a finish date newly backfilled. Run AFTER a library sync so the
// rows it dates already exist.
func (h *Handler) SyncKindleInsights(c *echo.Context) (kindleInsightsResponse, error) {
	var out kindleInsightsResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	svc := kindle.New(h.DB, amazon.NewStore(h.DB), h.Logger).
		SetHardcover(hardcover.NewStore(h.DB))
	n, err := svc.SyncInsights(c.Request().Context(), owner)
	if err != nil {
		// Surface the Amazon-side error so a cookie/format mismatch is debuggable
		// from the UI, exactly like the connect + kindle-sync flows.
		return out, apierr.BadRequest(err.Error())
	}
	h.Logger.Info("kindle insights synced", "user", owner, "datesBackfilled", n)
	return kindleInsightsResponse{DatesBackfilled: n, Source: "kindle"}, nil
}

// BackfillKindle enqueues the one-shot Kindle backfill for the caller. It runs on
// the jobs worker (owner-scoped payload) and returns the enqueued job id
// immediately. Idempotent to enqueue: the backfill upserts, so a duplicate run is
// harmless. Mirrors BackfillAudible.
func (h *Handler) BackfillKindle(c *echo.Context) (enqueuedJobResponse, error) {
	var out enqueuedJobResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if h.JobEnqueuer == nil {
		return out, apierr.BadRequest("background jobs are not available on this server")
	}
	// Confirm the user actually has an Amazon credential before enqueueing, so the
	// UI gets an immediate, clear error instead of a job that fails later.
	if _, lerr := amazon.NewStore(h.DB).Load(c.Request().Context(), owner); lerr != nil {
		return out, apierr.BadRequest("connect Amazon before running a backfill")
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), kindle.KindleBackfillKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return out, fmt.Errorf("kindle backfill enqueue failed: %w", eerr)
	}
	h.Logger.Info("kindle backfill enqueued", "user", owner, "jobId", id)
	return enqueuedJobResponse{Enqueued: true, JobID: id}, nil
}

// ReconcileKindle enqueues the one-shot Kindle STATUS reconcile sweep for the
// caller. Unlike sync/insights (single requests run inline) this is ~one CDE
// sidecar call per non-read book — thousands for a large library — so it runs on
// the jobs worker (owner-scoped payload, cap=1) and returns the enqueued job id
// immediately rather than blocking the request. It sets honest 'reading' status
// on non-read books that have a last-page-read record (leaving un-opened books
// 'want') and never clobbers a read/finished row. Idempotent to enqueue — a
// re-run re-polls every candidate and is a no-op on already-'reading' rows.
// Mirrors BackfillKindle.
func (h *Handler) ReconcileKindle(c *echo.Context) (enqueuedJobResponse, error) {
	var out enqueuedJobResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if h.JobEnqueuer == nil {
		return out, apierr.BadRequest("background jobs are not available on this server")
	}
	// Confirm the user actually has an Amazon credential before enqueueing, so the
	// UI gets an immediate, clear error instead of a job that fails later.
	if _, lerr := amazon.NewStore(h.DB).Load(c.Request().Context(), owner); lerr != nil {
		return out, apierr.BadRequest("connect Amazon before reconciling Kindle status")
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), kindle.KindleStatusReconcileKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return out, fmt.Errorf("kindle reconcile enqueue failed: %w", eerr)
	}
	h.Logger.Info("kindle status reconcile enqueued", "user", owner, "jobId", id)
	return enqueuedJobResponse{Enqueued: true, JobID: id}, nil
}
