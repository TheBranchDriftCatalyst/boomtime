package api

import "github.com/labstack/echo/v5"

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
	e.GET("/api/v1/amazon", h.GetAmazonConnection)
	e.DELETE("/api/v1/amazon", h.DisconnectAmazon)
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		e.POST("/api/v1/amazon/connect/start", h.ConnectAmazonStart)
		e.POST("/api/v1/amazon/connect/complete", h.ConnectAmazonComplete)
		e.POST("/api/v1/amazon/connect/import", h.ImportAmazonAuth)
		// Ingest + the siloed view/delete surface (data-deletion on request).
		e.POST("/api/v1/amazon/audible/sync", h.SyncAudible)
		e.POST("/api/v1/amazon/audible/backfill", h.BackfillAudible)
		// catalyst-books (Kindle) ingest triggers — the ebook mirror of audible/*.
		e.POST("/api/v1/kindle/sync", h.SyncKindle)
		e.POST("/api/v1/kindle/backfill", h.BackfillKindle)
		e.POST("/api/v1/kindle/insights", h.SyncKindleInsights)
		e.POST("/api/v1/kindle/reconcile", h.ReconcileKindle)
		e.GET("/api/v1/books/items", h.GetReadingItems)
		e.DELETE("/api/v1/books/items", h.DeleteReadingItemsHandler)
		// Book detail side panel: all editions of one canonical Work (rows sharing a
		// hardcover_book_id, or amazon_asin for unmatched siblings).
		e.GET("/api/v1/books/work", h.GetBookWork)
		// User-scoped reading-monitor status for the global nav indicator.
		e.GET("/api/v1/books/reading-monitor/status", h.ReadingMonitorStatus)
		// Delete one read from the history (reading_events) + propagate to Hardcover.
		e.DELETE("/api/v1/books/reads/:id", h.DeleteReadingEvent)
		// Curation override: set the effective status/rating/finish for one row
		// (boom-books, migration 00069) + enqueue the Hardcover push.
		e.PATCH("/api/v1/books/items/:externalId/curation", h.SetBookCuration)
		// Push-only: re-mirror one row's CURRENT effective state to Hardcover on demand.
		e.POST("/api/v1/books/items/:externalId/push", h.PushBookToHardcover)
		// Manual match-fixer: apply a user-chosen Hardcover book to a reading_item.
		e.POST("/api/v1/books/items/:externalId/match", h.SetBookManualMatch)
		// Interactive Hardcover catalog search (autocomplete for the match-fixer).
		e.GET("/api/v1/hardcover/search", h.HardcoverSearch)
		// Orchestrator: chain the whole reading-sync pipeline behind ONE enqueue.
		e.POST("/api/v1/books/sync-all", h.SyncAllBooks)

		// ── Liberation (boom-w20s.15) — the Libation rebuild ─────────────────
		// Nested under BooksEnabled AND gated on LiberationEnabled() (which also
		// requires a configured library path), so with the feature off these
		// paths 404 rather than existing and erroring. Same 404-when-off
		// convention the e2e specs rely on elsewhere.
		if h.Cfg.LiberationEnabled() {
			e.POST("/api/v1/books/items/:externalId/liberate", h.LiberateBook)
			e.DELETE("/api/v1/books/items/:externalId/liberate", h.ForgetLiberation)
			e.POST("/api/v1/books/liberate/sweep", h.SweepLiberation)
			e.GET("/api/v1/books/liberation/status", h.LiberationStatus)
			e.GET("/api/v1/books/liberation/excluded", h.LiberationExcluded)
		}
	}

	// Hardcover connect (catalyst-books PUSH target). GET/DELETE status ALWAYS
	// registered; the connect + pull + match MUTATION routes gate on BooksEnabled().
	e.GET("/api/v1/hardcover", h.GetHardcoverConnection)
	e.DELETE("/api/v1/hardcover", h.DisconnectHardcover)
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		e.POST("/api/v1/hardcover/connect", h.ConnectHardcover)
		// Inbound sync (PULL half): read the shelf + reconcile linkage.
		e.POST("/api/v1/hardcover/pull", h.PullHardcover)
		// Explicit MATCH stage: resolve unmatched reading_items to a Hardcover book.
		e.POST("/api/v1/hardcover/match", h.MatchHardcover)
	}
}
