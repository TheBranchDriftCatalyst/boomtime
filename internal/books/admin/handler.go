// Package admin is the catalyst-books ADMIN HTTP surface (gaka-zp2s), extracted
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
	// gaka-books: admin diagnostic — dump raw Audible/Kindle source data.
	g.GET("/books/diagnostics", h.AdminBooksDiagnostics)

	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		// gaka-books: admin LIVE Kindle reading-monitor WS (cookie-authed +
		// admin-gated in-handler). Read-only; never persists positions.
		g.GET("/books/reading-monitor/ws", h.AdminBooksReadingMonitorWS)
		// gaka-books §5.1: the PERSISTENT server-side monitor's view+toggle.
		g.GET("/books/reading-monitor", h.AdminBooksReadingMonitorGet)
		g.PUT("/books/reading-monitor", h.AdminBooksReadingMonitorPut)
		// Raw diagnostic stream: recent raw samples for BOTH reading sources.
		g.GET("/books/reading-monitor/raw", h.AdminBooksReadingMonitorRaw)
	}
}
