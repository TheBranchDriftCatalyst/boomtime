// Package admin is the catalyst-books ADMIN HTTP surface (boom-zp2s), extracted
// from the central internal/admin god-package so the books domain owns its own
// operator endpoints (Django-app-style per-domain admin/ seam folder). It is
// mounted through books.Module.RegisterAdminRoutes — the peer of the portable
// jobs.RegisterAdminRoutes seam — rather than being hand-wired into internal/admin.
//
// The endpoints:
//   - GET  /api/v1/admin/books/diagnostics              raw Audible/Kindle source dump
//   - GET  /api/v1/admin/books/reading-monitor/ws       live Kindle position monitor (WS)
//   - GET  /api/v1/admin/books/reading-monitor          persistent-monitor view
//   - PUT  /api/v1/admin/books/reading-monitor          persistent-monitor toggle
//   - GET  /api/v1/admin/books/reading-monitor/raw      raw sample stream (both sources)
//
// Deps are the minimal set these endpoints read (DB/Cfg/Logger). Owner resolution
// + admin gating go through apihelpers + cfg.IsAdmin, exactly as the pre-move
// receivers on *admin.Handler did — behavior is byte-identical.
package admin

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// Handler bundles the SUBSET of dependencies the books-admin endpoints read.
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
}

// New constructs a books-admin Handler with the shared core deps.
func New(database *db.DB, cfg *config.Config, logger *slog.Logger) *Handler {
	return &Handler{DB: database, Cfg: cfg, Logger: logger}
}

// requireAdmin: 401 without a token, 403 when not on the admin allowlist. Returns
// the resolved owner on success. Byte-identical to the copy on *admin.Handler and
// the other per-domain admin guards — a shared helper would need DI scaffolding
// bigger than the 8-line body itself.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}

// Register mounts the books-admin endpoints onto g (whose prefix the host anchors
// at /api/v1/admin). Route strings + BooksEnabled() gating are byte-identical to
// the pre-move registrations in internal/admin/routes.go:
//   - diagnostics is ALWAYS registered (matches the pre-move unconditional line).
//   - the reading-monitor cluster registers ONLY when BOOM_FEATURE_BOOKS is on, so
//     the paths 404 like any unknown route when the feature is off.
//
// Nil-safe: the OpenAPI drift router drives this with a zero-value handler (nil
// Cfg), so the BooksEnabled branch is skipped and only diagnostics enumerates —
// exactly as before the move.
func Register(g *echo.Group, h *Handler) {
	// boom-books: admin diagnostic — dump raw Audible/Kindle source data.
	apiroute.GGET(g, "/books/diagnostics", h.AdminBooksDiagnostics).
		Doc("Books source diagnostics",
			"Signs raw probe requests with the caller's OWN connected Amazon device "+
				"credential and returns each response verbatim — the HTTP status plus parsed "+
				"JSON when the body is valid JSON, or raw text when it is XML or an error "+
				"page — so every field a source exposes can be inventoried before anything "+
				"is committed to the reading model. This is also how the reverse-engineered "+
				"request signing gets verified against each host. ?source= picks audible "+
				"(the default; widest response_groups plus listening-stats), kindle (Fiona "+
				"library metadata and whispersync datasets), or liberation. audible and "+
				"kindle are pure dumps; liberation instead checks the download-protocol "+
				"assumptions live and fills in verdict (pass, warn, fail or skip) and a "+
				"human-readable detail on each probe, and honours ?asin= to scope the check "+
				"to one title rather than the first library item. Non-JSON bodies are "+
				"truncated at 20000 characters. 400 when the caller has no Amazon "+
				"credential; admin only, so 403 for anyone off the allowlist.")

	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		// boom-books: admin LIVE Kindle reading-monitor WS (cookie-authed +
		// admin-gated in-handler). Read-only; never persists positions.
		apiroute.GWebSocket(g, "/books/reading-monitor/ws", h.AdminBooksReadingMonitorWS).
			Doc("Live Kindle position monitor",
				"A WebSocket that streams the furthest-page-read position of the caller's "+
					"in-progress Kindle books while a client stays connected: open a book on "+
					"the device, read, and watch the location advance — an empirical probe of "+
					"the whispersync cadence. Purely observational; it never writes reading "+
					"positions or activity. Authenticated by the HttpOnly refresh_token COOKIE "+
					"(a handshake cannot carry an Authorization header) and admin-gated in the "+
					"handler. Every frame is one JSON object discriminated by type: info once "+
					"on connect (the effective cadence), sample when a book's position was "+
					"first seen or has ADVANCED (unchanged positions are deduped), heartbeat "+
					"once per cycle even when nothing moved, and error for a per-book or "+
					"listing failure — the stream continues past those. ?interval= is seconds, "+
					"default 6, clamped to 2-60; ?limit= is books polled per cycle, default 12, "+
					"capped at 50. It polls once immediately and then on the interval, and "+
					"auto-stops after 20 minutes so a forgotten tab cannot poll Amazon forever. "+
					"Answers 101 and carries no HTTP body, so Swagger cannot exercise it.")
		// boom-books §5.1: the PERSISTENT server-side monitor's view+toggle. Both
		// halves answer the SAME readingMonitorView — the PUT re-reads the view
		// after writing — which is why they share the type rather than merely
		// resembling each other.
		apiroute.GGET(g, "/books/reading-monitor", h.AdminBooksReadingMonitorGet).
			Doc("Persistent reading-monitor state",
				"The persistent server-side reading monitor as it stands for the caller: the "+
					"stored enabled flag and mode (debounced or verbose), how many of their "+
					"books are currently in fine-capture, the most recent poll time (RFC3339, "+
					"or null when it has never run), whether a calibration burst is active and "+
					"when it expires, and the derived recommendation — the optimal poll "+
					"intervals plus a sync-pattern classification, null until enough advances "+
					"have been observed to calibrate. This is a thin VIEW over persisted "+
					"state, not the engine: the engine runs on the leader-singleton scheduler "+
					"whether or not anyone has this panel open. Admin only. The unprivileged "+
					"equivalent for the nav indicator is "+
					"GET /api/v1/books/reading-monitor/status.")
		// GWritesJSON, not GPUT: the handler binds UNCAPPED (plain c.Bind) and GPUT
		// would silently shrink that to the seam's 4 KiB. See the handler comment.
		apiroute.GWritesJSON[readingMonitorView](g, http.MethodPut, "/books/reading-monitor", h.AdminBooksReadingMonitorPut).
			Doc("Update the reading monitor",
				"Body {enabled?, mode?, calibrate?}: every field is optional so a client can "+
					"flip one without disturbing the others, but sending none of them is a 400 "+
					"rather than a silent no-op. mode must be debounced or verbose. "+
					"calibrate:true opens a high-fidelity burst window of the configured "+
					"duration starting now; false cancels one early — the engine reverts on its "+
					"own when the window expires, so false is only ever an early stop. Answers "+
					"200 with the SAME view the GET returns, re-read after the write, so the "+
					"panel needs no follow-up request. 404 when the user row is missing. Admin "+
					"only. The body is bound in the handler and UNCAPPED, so no request schema "+
					"is generated for it.")
		// Raw diagnostic stream: recent raw samples for BOTH reading sources.
		apiroute.GGET(g, "/books/reading-monitor/raw", h.AdminBooksReadingMonitorRaw).
			Doc("Raw reading samples",
				"The raw data underneath BOTH reading sources, side by side, for the "+
					"diagnostic page. kindle is up to 200 recent last-page-read position "+
					"samples; each carries the change in location (dloc) and the seconds since "+
					"that SAME book's previous sample, both 0 on a book's first sample, and the "+
					"combined list is presented newest-first across all books. audible is the "+
					"per-day listening-seconds buckets from the last 30 days. Neither is "+
					"paginated and neither is cached — this is the unaggregated stream the "+
					"two-level monitor engine samples. Admin only.")
	}
}
