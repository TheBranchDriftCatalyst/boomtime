package api

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register mounts the catalyst-books HTTP surface (boom-zp2s Phase 2). Extracted
// verbatim from internal/identity/routes.go — same routes, same order, same
// BooksEnabled() gating — so the API is byte-identical. Called by the composition
// root after the identity routes (book paths never overlap non-book paths, so the
// relative call order is immaterial for matching). Owner resolution inside the
// handlers still goes through apihelpers.IdentifyOwner.
func Register(e *echo.Echo, h *Handler) {
	// Amazon device connect (catalyst-books + catalyst-audiobooks share ONE Amazon
	// link). GET/DELETE status are ALWAYS registered (GET reports {connected:false},
	// DELETE is a no-op when nothing is stored, credential NEVER returned). The
	// connect + import MUTATION routes gate on Cfg.BooksEnabled() — inert on default boot.
	apiroute.GET(e, "/api/v1/amazon", h.GetAmazonConnection)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/amazon", h.DisconnectAmazon)
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		apiroute.POST(e, "/api/v1/amazon/connect/start", h.ConnectAmazonStart)
		// complete + import bind their OWN bodies (16 KiB / 64 KiB caps the seam's
		// binding registrars cannot express), so they register as 204-no-body.
		apiroute.NoContent(e, http.MethodPost, "/api/v1/amazon/connect/complete", h.ConnectAmazonComplete)
		apiroute.NoContent(e, http.MethodPost, "/api/v1/amazon/connect/import", h.ImportAmazonAuth)
		// Ingest + the siloed view/delete surface (data-deletion on request).
		apiroute.POSTNoBody(e, "/api/v1/amazon/audible/sync", h.SyncAudible)
		apiroute.Accepted(e, http.MethodPost, "/api/v1/amazon/audible/backfill", h.BackfillAudible)
		// catalyst-books (Kindle) ingest triggers — the ebook mirror of audible/*.
		apiroute.POSTNoBody(e, "/api/v1/kindle/sync", h.SyncKindle)
		apiroute.Accepted(e, http.MethodPost, "/api/v1/kindle/backfill", h.BackfillKindle)
		apiroute.POSTNoBody(e, "/api/v1/kindle/insights", h.SyncKindleInsights)
		apiroute.Accepted(e, http.MethodPost, "/api/v1/kindle/reconcile", h.ReconcileKindle)
		apiroute.GET(e, "/api/v1/books/items", h.GetReadingItems)
		apiroute.DELETE(e, "/api/v1/books/items", h.DeleteReadingItemsHandler)
		// Book detail side panel: all editions of one canonical Work (rows sharing a
		// hardcover_book_id, or amazon_asin for unmatched siblings).
		apiroute.GET(e, "/api/v1/books/work", h.GetBookWork)
		// User-scoped reading-monitor status for the global nav indicator.
		apiroute.GET(e, "/api/v1/books/reading-monitor/status", h.ReadingMonitorStatus)
		// Delete one read from the history (reading_events) + propagate to Hardcover.
		apiroute.DELETE(e, "/api/v1/books/reads/:id", h.DeleteReadingEvent)
		// Curation override: set the effective status/rating/finish for one row
		// (boom-books, migration 00069) + enqueue the Hardcover push.
		apiroute.PATCH(e, "/api/v1/books/items/:externalId/curation", h.SetBookCuration)
		// Push-only: re-mirror one row's CURRENT effective state to Hardcover on demand.
		// Stays on plain e.POST: 200 readingItemDTO inline vs 202 {enqueued} on the
		// queue fallback — two statuses and two shapes one registration cannot state.
		e.POST("/api/v1/books/items/:externalId/push", h.PushBookToHardcover)
		// Manual match-fixer: apply a user-chosen Hardcover book to a reading_item.
		// Stays on plain e.POST: 200 readingItemDTO normally, 200 {matched,
		// hardcoverBookId} when the post-write read-back misses.
		e.POST("/api/v1/books/items/:externalId/match", h.SetBookManualMatch)
		// Interactive Hardcover catalog search (autocomplete for the match-fixer).
		apiroute.GET(e, "/api/v1/hardcover/search", h.HardcoverSearch)
		// Orchestrator: chain the whole reading-sync pipeline behind ONE enqueue.
		apiroute.Accepted(e, http.MethodPost, "/api/v1/books/sync-all", h.SyncAllBooks)

		// ── Liberation (boom-w20s.15) — the Libation rebuild ─────────────────
		// Nested under BooksEnabled AND gated on LiberationEnabled() (which also
		// requires a configured library path), so with the feature off these
		// paths 404 rather than existing and erroring. Same 404-when-off
		// convention the e2e specs rely on elsewhere.
		if h.Cfg.LiberationEnabled() {
			apiroute.Accepted(e, http.MethodPost, "/api/v1/books/items/:externalId/liberate", h.LiberateBook)
			apiroute.DELETE(e, "/api/v1/books/items/:externalId/liberate", h.ForgetLiberation)
			// Accepted (not AcceptedBody): the sweep's optional body must keep its
			// tolerate-anything binding — see SweepLiberation's comment.
			apiroute.Accepted(e, http.MethodPost, "/api/v1/books/liberate/sweep", h.SweepLiberation)
			// Typed seam (internal/shared/apiroute): the response TYPE is
			// captured here, so the OpenAPI schema is generated from Go rather
			// than hand-written. Registering these with plain e.GET no longer
			// compiles — which is the point.
			apiroute.GET(e, "/api/v1/books/liberation/status", h.LiberationStatus)
			apiroute.GET(e, "/api/v1/books/liberation/excluded", h.LiberationExcluded)
		}
	}

	// Hardcover connect (catalyst-books PUSH target). GET/DELETE status ALWAYS
	// registered; the connect + pull + match MUTATION routes gate on BooksEnabled().
	apiroute.GET(e, "/api/v1/hardcover", h.GetHardcoverConnection)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/hardcover", h.DisconnectHardcover)
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		apiroute.NoContentBody(e, http.MethodPost, "/api/v1/hardcover/connect", h.ConnectHardcover)
		// Inbound sync (PULL half): read the shelf + reconcile linkage.
		apiroute.Accepted(e, http.MethodPost, "/api/v1/hardcover/pull", h.PullHardcover)
		// Explicit MATCH stage: resolve unmatched reading_items to a Hardcover book.
		apiroute.Accepted(e, http.MethodPost, "/api/v1/hardcover/match", h.MatchHardcover)
	}
}
