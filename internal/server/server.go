// Package server wires the Echo router, registers all routes in hakatime's order
// (Api.hs), and serves the embedded SPA as a fallback for non-API routes.
package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/tracing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/catalyst"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/meta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/queryapi"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

//go:embed dist
var distFS embed.FS

// New builds a configured Echo server. logHub streams server-process slog
// records to the Logs tab; pass nil to disable that endpoint's live stream.
//
// (See NewWithHandler if the caller needs to attach additional dependencies
// to the constructed *handler.Handler — this shape is preserved for
// backward compatibility with existing tests that only care about the
// Echo instance.)
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, logHub *logging.LogHub, reg *catalyst.Registry) *echo.Echo {
	e, _ := NewWithHandler(database, cfg, logger, logHub, reg)
	return e
}

// NewWithHandler is New but also returns the constructed *handler.Handler
// so callers (cmd/boomtime) can wire post-construction dependencies like
// the label-images worker.
func NewWithHandler(database *db.DB, cfg *config.Config, logger *slog.Logger, logHub *logging.LogHub, reg *catalyst.Registry) (*echo.Echo, *handler.Handler) {
	e := echo.New()

	e.Use(middleware.Recover())
	// OpenTelemetry server spans (TALOS-kvg1). Registered early so every
	// request is traced and the span context is available to CORS/auth/
	// handlers downstream. No-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set.
	e.Use(tracing.Middleware())
	// gaka-n5r: CORS is credentialed (AllowCredentials=true is required so the
	// refresh_token cookie flows behind the Vite proxy), which means the
	// Access-Control-Allow-Origin value MUST be a checked allowlist entry — the
	// previous reflect-any-origin behaviour let attacker pages read the login
	// response body (and its fresh access token). Origins come from
	// BOOM_CORS_ALLOWED_ORIGINS; if unset in dev we fall back to localhost:5173
	// + localhost:8080; if unset in prod we already refused to start in
	// cmd/boomtime, so allowedOrigins here is guaranteed non-empty in that case.
	allowedOrigins := parseAllowedOrigins(os.Getenv("BOOM_CORS_ALLOWED_ORIGINS"), logger)
	if len(allowedOrigins) == 0 {
		allowedOrigins = defaultDevAllowedOrigins
		logger.Warn("BOOM_CORS_ALLOWED_ORIGINS not set — falling back to localhost dev origins",
			"origins", allowedOrigins,
			"remediation", "set BOOM_CORS_ALLOWED_ORIGINS=https://your.domain in prod")
	} else {
		logger.Info("CORS allowlist configured", "origins", allowedOrigins)
	}
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		// Exact-match allowlist (see internal/server/cors.go). We stay on
		// UnsafeAllowOriginFunc rather than AllowOrigins because echo's default
		// matcher uses strings.EqualFold, and we want case-sensitive scheme
		// checks (an attacker who registers HTTP://LOCALHOST:5173 shouldn't
		// squeak through a case-fold match).
		UnsafeAllowOriginFunc: func(_ *echo.Context, origin string) (string, bool, error) {
			if isOriginAllowed(origin, allowedOrigins) {
				return origin, true, nil
			}
			return "", false, nil
		},
		AllowHeaders:     []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization, "X-Machine-Name"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowCredentials: true,
	}))
	if cfg.HTTPLog {
		e.Use(requestLogger(logger))
	}
	// Router rate-metric decorations (internal/metrics): unconditional and
	// cheap (a few counter bumps per request), feeding the admin Metrics
	// dashboard. Independent of HTTPLog — the graph is useful even when
	// per-request logging is off.
	e.Use(metricsMiddleware())
	if cfg.DBN1Threshold > 0 || cfg.DBN1DupThresh > 0 {
		e.Use(n1Middleware(logger, cfg.DBN1Threshold, cfg.DBN1DupThresh))
	}
	// Universal rate limit (gaka-jk6 / gaka-ddp / gaka-awh.1). Installed
	// AFTER CORS (so preflight can short-circuit inside the middleware
	// without ever counting against a bucket) and BEFORE the handler
	// registration (so it wraps every route, including auth writes and
	// wakatime_key probe endpoints). See internal/server/ratelimit.go for
	// bucket sizing, testing hook (BOOM_DISABLE_RATE_LIMIT=1), and TTL /
	// cleanup notes.
	installRateLimit(e, logger, database)
	// gaka-ar7: stash resolved owner in ctx so the pgx tracer can tag its DEBUG
	// SQL records with "user" — LogHub's FilterForUser then gates them per tenant.
	e.Use(userCtxMiddleware(database))

	// GET /metrics — the Prometheus scrape endpoint (internal/metrics.Registry).
	// Deliberately unauthenticated and off the rate-limit + request-log paths:
	// it is scraped intra-cluster by Prometheus (same posture as /healthz), and
	// the middleware chain skips it by path (see metricsMiddleware,
	// requestLogger, and the rate-limit bypass). No user data is exposed — only
	// aggregate service metrics.
	e.GET("/metrics", func(c *echo.Context) error {
		metrics.Handler().ServeHTTP(c.Response(), c.Request())
		return nil
	})

	h := handler.New(database, cfg, logger, logHub)
	registerRoutes(e, h, reg)
	registerStatic(e, h, cfg, logger)
	return e, h
}

// registerRoutes wires all API routes, one registration func per domain. The
// call order (and the order within each func) preserves the original flat
// registration sequence.
func registerRoutes(e *echo.Echo, h *handler.Handler, reg *catalyst.Registry) {
	// gaka-zp2s: the composition root threads ONE catalyst.Registry (built in
	// internal/domainreg) through here so per-domain wiring flows via the Module
	// contract instead of this package naming each domain. deps carries the core
	// deps a Module needs to build its handlers; the OpenAPI drift router passes a
	// zero-value handler, so these are nil there and each Module's nil-guards keep
	// the enumerated route set identical.
	deps := catalyst.Deps{DB: h.DB, Cfg: h.Cfg, Logger: h.Logger, Cache: h.Cache}
	// gaka-zp2s: the boomtime data-domain HTTP surface (ingest / curation / stats /
	// widgets / goals / spaces / awards) is no longer named here — it moved onto
	// boomtime.Module.RegisterRoutes (driven by the reg.Modules() loop below), so this
	// package imports no boomtime data domain. Route strings + order are byte-identical;
	// the module registers them at the module-loop slot and the domains don't overlap,
	// so the routing tree (and the drift-guard route set) is unchanged.
	// gaka-8tn phase 7: admin domain (label-images admin + git-history
	// backfill + whole-DB backup export/import + wakatime.com import
	// cluster + source-health observability + the public label-image GET
	// that is the read-only face of the same subsystem). `admin.Register`
	// fans out the 24 routes formerly split between registerImportRoutes,
	// registerMiscRoutes, registerStatsRoutes (the backup pair), and
	// registerHeartbeatRoutes (source-health). Route strings + order
	// preserved verbatim.
	admin.Register(e, h.Admin)
	// gaka-zp2s: per-domain ADMIN surfaces are mounted through the Module
	// contract (Module.RegisterAdminRoutes) — the peer of the portable
	// jobs.RegisterAdminRoutes seam — instead of being hand-wired into
	// internal/admin. Anchored at /api/v1/admin; each domain sub-registers its
	// own paths (books → /api/v1/admin/books/*). Driven HERE (not
	// post-construction) so the OpenAPI drift router — which calls registerRoutes
	// with a zero-value handler — enumerates the same routes, and so the module
	// routes register BEFORE registerStatic's "/*" catch-all. boomtime/github
	// contribute no-op admin surfaces today (BaseModule default); books mounts
	// its diagnostics + reading-monitor cluster.
	adminGroup := e.Group("/api/v1/admin")
	for _, m := range reg.Modules() {
		m.RegisterAdminRoutes(adminGroup, deps)
	}
	// gaka-8tn phase 1: meta + logs registration is now owned by the meta
	// domain package. `meta.Register` fans out /api/v1/version,
	// /api/v1/changelog, /healthz, the OpenAPI spec + Swagger UI, and the
	// /api/v1/logs REST + WS endpoints. Order preserved: same effective
	// route set as pre-refactor registerLogRoutes + registerMetaRoutes.
	meta.Register(e, h.Meta)
	// gaka-8tn phase 4a: identity (auth + password + profile + timezone +
	// wakatime_key + avatar) extracted into internal/identity. Identity is an
	// intentional infra peer (not a boomtime data domain), so it stays wired here.
	identity.Register(e, h.Identity)
	// gaka-zp2s: per-domain HTTP surfaces are mounted through the Module contract
	// (Module.RegisterRoutes) instead of being named here. Today books mounts its
	// Amazon/Kindle/Audible + Hardcover + reading-items surface (into internal/books/api)
	// and stashes its handler for late-wiring; boomtime/github contribute no-op
	// RegisterRoutes until their seam extraction (steps 3+). Registered after identity;
	// book paths never overlap identity paths, so relative order is immaterial.
	for _, m := range reg.Modules() {
		m.RegisterRoutes(e, deps)
	}
	// gaka-174.q: the cross-domain query DSL HTTP surface (POST /api/v1/query).
	// Auth-required + owner-scoped; the reading domain is gated behind
	// Cfg.BooksEnabled() inside the handler (runtime, since the domain is a
	// body field). coding is always available.
	queryapi.Register(e, h.Query)
}

// gaka-zp2s: the phase-8 no-op route stubs (registerGoalRoutes /
// registerHeartbeatRoutes / registerCurationRoutes / registerStatsRoutes /
// registerMiscRoutes / registerImportRoutes) are gone — every boomtime data-domain
// route now registers via boomtime.Module.RegisterRoutes, so this package names no
// boomtime data domain at all.

// registerStatic serves the SPA: from BOOM_DASHBOARD_PATH on disk if set, else
// from the embedded dist FS. Non-API routes fall back to index.html.
func registerStatic(e *echo.Echo, h *handler.Handler, cfg *config.Config, logger *slog.Logger) {
	var fsys fs.FS
	if cfg.DashboardPath != "" {
		logger.Info("serving dashboard from disk", "path", cfg.DashboardPath)
		fsys = os.DirFS(cfg.DashboardPath)
	} else {
		sub, err := fs.Sub(distFS, "dist")
		if err != nil {
			logger.Error("failed to open embedded dist", "err", err)
			return
		}
		fsys = sub
	}

	// gaka social-card: server-side OpenGraph injection for /p/:slug. Registered
	// BEFORE the SPA catch-all so Echo's param route wins over "/*". For a
	// PUBLIC profile we replace the index.html <!--OG_META--> block with
	// per-user og:*/twitter:* tags (title/description from public stats, an
	// ABSOLUTE og:image → the og.png endpoint) so Discord/Slack/Twitter unfurl
	// a rich card; the SPA then hydrates over the same shell for real browsers.
	// Non-public/unknown slug → the shell's generic default block is served
	// unchanged (no oracle).
	e.GET("/p/:slug", func(c *echo.Context) error {
		shell, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			// No shell to serve (dist not built) — fall through to the file
			// server which will 404/redirect as usual.
			c.Request().URL.Path = "/"
			http.FileServer(http.FS(fsys)).ServeHTTP(c.Response(), c.Request())
			return nil
		}
		if meta, ok := h.Identity.BuildOGMeta(c.Request().Context(), c.Param("slug"), publicBaseURL(c, cfg)); ok {
			shell = injectOGMeta(shell, meta)
		}
		// Same shell cache policy as the catch-all: revalidate every load so
		// hashed-chunk imports never go stale.
		c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
		return c.HTMLBlob(http.StatusOK, shell)
	})

	fileServer := http.FileServer(http.FS(fsys))
	e.GET("/*", func(c *echo.Context) error {
		reqPath := strings.TrimPrefix(c.Request().URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		servingShell := false
		if _, err := fs.Stat(fsys, reqPath); err != nil {
			// Missing file. Return 404 for anything that looks like an asset
			// (i.e. the last path segment has a file extension). Reason: a
			// stale-cached client requesting an old chunk hash like
			// /assets/Settings-OLDHASH.js would otherwise get index.html
			// served with 200 OK, then try to parse HTML as JavaScript and
			// silently fail — breaking the whole lazy-loaded route with no
			// user-visible error. Real routes (no extension in the last
			// segment) still fall back to index.html so client-side
			// routing keeps working.
			if strings.Contains(path.Base(reqPath), ".") {
				return echo.NewHTTPError(http.StatusNotFound, "not found")
			}
			c.Request().URL.Path = "/"
			servingShell = true
		} else if reqPath == "index.html" {
			servingShell = true
		}
		if servingShell {
			// The SPA shell embeds hashed chunk names via dynamic imports;
			// every deploy the shell must revalidate or clients ride the
			// stale hashes and lazy-loaded routes 404. Asset files keep
			// the default (immutable-ish via hashed filenames) — only the
			// shell revalidates. no-cache means "revalidate every load",
			// which is basically free because the shell is ~3 KB.
			c.Response().Header().Set("Cache-Control", "no-cache, must-revalidate")
		}
		fileServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})
}

// ogMetaBlockRe matches the injectable OG block in the SPA shell — from the
// opening "<!--OG_META" comment through the closing "<!--/OG_META-->". The Go
// server replaces the whole span with per-user tags for a public /p/:slug.
var ogMetaBlockRe = regexp.MustCompile(`(?s)<!--OG_META.*?<!--/OG_META-->`)

// injectOGMeta swaps the shell's <!--OG_META…/OG_META--> block for per-user
// og:*/twitter:* tags. All dynamic values are HTML-attribute-escaped. If the
// marker is absent (older shell) the shell is returned unchanged rather than
// risking a malformed <head>.
func injectOGMeta(shell []byte, meta *identity.OGMeta) []byte {
	if !ogMetaBlockRe.Match(shell) {
		return shell
	}
	esc := htmlAttr
	var b strings.Builder
	b.WriteString(`<meta property="og:type" content="profile" />`)
	b.WriteString(`<meta property="og:site_name" content="boomtime" />`)
	fmt.Fprintf(&b, `<meta property="og:title" content="%s" />`, esc(meta.Title))
	fmt.Fprintf(&b, `<meta property="og:description" content="%s" />`, esc(meta.Description))
	fmt.Fprintf(&b, `<meta property="og:image" content="%s" />`, esc(meta.ImageURL))
	fmt.Fprintf(&b, `<meta property="og:image:width" content="1200" />`)
	fmt.Fprintf(&b, `<meta property="og:image:height" content="630" />`)
	fmt.Fprintf(&b, `<meta property="og:url" content="%s" />`, esc(meta.ProfileURL))
	b.WriteString(`<meta name="twitter:card" content="summary_large_image" />`)
	fmt.Fprintf(&b, `<meta name="twitter:title" content="%s" />`, esc(meta.Title))
	fmt.Fprintf(&b, `<meta name="twitter:description" content="%s" />`, esc(meta.Description))
	fmt.Fprintf(&b, `<meta name="twitter:image" content="%s" />`, esc(meta.ImageURL))
	return ogMetaBlockRe.ReplaceAll(shell, []byte(b.String()))
}

// htmlAttr escapes a string for safe inclusion inside a double-quoted HTML
// attribute value (og:* content). Covers the five significant characters.
func htmlAttr(s string) string {
	return htmlAttrReplacer.Replace(s)
}

var htmlAttrReplacer = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
)

// publicBaseURL resolves the absolute public origin (scheme://host, no trailing
// slash) for building absolute og:image/og:url values. Prefers the configured
// BadgeURL (the same public origin widget embed URLs use); otherwise derives it
// from the request (honouring X-Forwarded-Proto behind a proxy).
func publicBaseURL(c *echo.Context, cfg *config.Config) string {
	if cfg.BadgeURL != "" {
		return strings.TrimRight(cfg.BadgeURL, "/")
	}
	req := c.Request()
	scheme := "http"
	if proto := req.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if req.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + req.Host
}
