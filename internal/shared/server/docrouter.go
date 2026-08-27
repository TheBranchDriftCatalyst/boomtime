package server

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	sharedadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/shared/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/handler"
)

// DocumentationRouter builds an ENUMERATION-ONLY router carrying every route the
// binary can serve, regardless of how this instance is configured.
//
// It is never started and never serves a request. Its handlers would fault if
// called — the DB has a nil pool and the per-domain bags are mostly nil — but
// registration never touches either, so walking Routes() is safe.
//
// WHY NOT WALK THE LIVE ROUTER. Because the live router is config-specific:
// routes are gated by `if` blocks, so a flag that is off makes routes vanish
// from the docs. That is precisely how the whole books domain went undocumented
// while a bidirectional drift guard reported success (boom-i18f). The spec
// should describe the API this binary implements, not the subset one deployment
// happens to have switched on.
//
// The tradeoff, stated plainly: Swagger UI may list a route that 404s on THIS
// server. That is the better failure — a documented route that is off is
// discoverable and explains itself, an undocumented route is invisible.
func DocumentationRouter(reg *catalyst.Registry) *echo.Echo {
	e := echo.New()
	registerRoutes(e, documentationHandler(), reg)
	return e
}

// documentationHandler is the fixture handler the doc router registers against.
//
// EVERY BAG A DOMAIN GATES ON MUST BE POPULATED, because each Register func
// reads Cfg off ITS OWN bag, not off the top-level handler. Leave one nil and
// that domain's gated routes vanish with no error — the failure mode this whole
// exercise exists to eliminate. Known gates today:
//   - Identity.Cfg   → GitHub connect (4 routes), notifications (2)
//   - Admin.DB       → jobs (9) + metrics (1); these gate on DEPENDENCY
//     availability, not on a feature flag
//   - Books/boomtime → gated via catalyst.Deps, which carries the top-level Cfg
//
// Built with STRUCT LITERALS rather than each domain's New(): the constructors
// have side effects (identity.New dials an S3 client for the social-card cache
// and logs on failure), and an enumeration fixture must not reach the network or
// depend on a logger it has no reason to own.
//
// A nil-pool *db.DB satisfies the dependency checks without opening a
// connection — registration only closes over the handle to build RequireCap
// middleware, it never queries.
func documentationHandler() *handler.Handler {
	cfg := config.DocumentationFixture()
	database := &db.DB{} // non-nil, nil pool — never dialed
	return &handler.Handler{
		DB:       database,
		Cfg:      cfg,
		Admin:    &sharedadmin.Handler{DB: database, Cfg: cfg},
		Identity: &identity.Handler{DB: database, Cfg: cfg},
	}
}
