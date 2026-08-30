package server

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/domainreg"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	sharedadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/shared/admin"
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
// TAKES NO REGISTRY, DELIBERATELY. It builds its OWN via domainreg.Build(), and
// that is a correctness requirement rather than a convenience.
//
// catalyst modules are STATEFUL across registration: books.Module.RegisterRoutes
// does `m.h = booksapi.New(...)` and boomtime.Module does the same for m.admin,
// stashing the handler so the composition root can late-wire a jobs enqueuer onto
// it once the provider exists. Handing this function the LIVE registry therefore
// runs RegisterRoutes a SECOND time on the same module instances and replaces
// those stashed handlers with ones built from the documentation fixture. The
// enqueuer then lands on the fixture handler while the live routes still hold
// method values bound to the original — so every background-job enqueue answers
// "background jobs are not available on this server" while the jobs page itself
// looks perfectly healthy.
//
// That shipped (c638283) and reached production. Building the registry here means
// the live one cannot be passed by mistake.
func DocumentationRouter() *echo.Echo {
	e := echo.New()
	registerRoutes(e, documentationHandler(), domainreg.Build().Registry)
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
