package meta

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/openapi"
	"github.com/labstack/echo/v5"
)

// Register wires the meta domain's routes onto e. Called from
// internal/server/server.go as `meta.Register(e, h.Meta)` — replacing the
// inline registerMetaRoutes + registerLogRoutes helpers that used to live
// in the server package. Registration order is preserved verbatim from the
// pre-refactor server.go so tests + traffic see the exact same matching.
//
// All routes here are intentionally UNAUTHENTICATED at the router layer;
// the handlers themselves gate on the Authorization header (ServerLogs) or
// the refresh_token cookie (ServerLogsWS). /healthz, /api/v1/version, and
// /api/v1/changelog are public by design — see the individual handler
// files for rationale.
//
// Everything is registered through internal/shared/apiroute so the OpenAPI
// spec picks up the real Go response types and the prose below, rather than
// the generic `{"type":"object"}` stub. Note the three NON-JSON forms:
// /api/v1/changelog is Raw text/markdown and /api/v1/logs/ws is a WebSocket
// handshake (101, no body) — documenting either as a JSON object would be
// wrong, not merely vague.
func Register(e *echo.Echo, h *Handler) {
	// Meta cluster: version disclosure, embedded changelog, health probe,
	// plus the self-hosted OpenAPI spec + Swagger UI (boom-lfc). The
	// OpenAPI registration doesn't touch the meta Handler — it's colocated
	// here because it shares the "public transparency" audience.
	apiroute.GET(e, "/api/v1/version", h.Version).
		Doc("Running build version",
			"The git-describe string stamped into the binary at build time via ldflags, "+
				"or the literal \"dev\" when the binary was not stamped (a plain `go run` / "+
				"local build). Unauthenticated by design: version disclosure on a self-hosted "+
				"app is the same low-risk posture as /healthz and /badge/*. The body is only "+
				"{\"version\": string} — for branch, commit, build time, uptime and DB state "+
				"call GET /healthz instead.")
	apiroute.Raw(e, http.MethodGet, "/api/v1/changelog", "text/markdown", http.StatusOK, h.Changelog).
		Doc("Embedded release changelog",
			"Serves the repository's CHANGELOG.md verbatim as text/markdown (charset=utf-8) — "+
				"NOT JSON. The bytes are compiled into the binary, so every request returns the "+
				"identical payload until the next release and the frontend parses it client-side "+
				"(web/shared/lib/changelog.ts). Unauthenticated, same public audience as "+
				"/api/v1/version.")
	apiroute.GET(e, "/healthz", h.Healthz).
		Doc("Liveness, build and DB probe",
			"Unauthenticated probe for container orchestrators and uptime monitors. It ALWAYS "+
				"answers 200, even when the database is down — status is \"ok\" when a 2-second "+
				"pool Ping succeeds and \"degraded\" when it does not, so a caller must inspect "+
				"the body rather than the status code to tell 'process alive' from 'DB up'. "+
				"Returns version/branch/commit/buildTime (omitted on an un-stamped build; "+
				"unlike /api/v1/version there is no \"dev\" substitution here), startedAt as "+
				"RFC3339 UTC plus uptimeSeconds, dbReachable, the applied migration "+
				"schemaVersion (0 when the DB is unreachable), and a features map of the "+
				"effective substrate switches — user_model on|off, auth_provider local|oidc, "+
				"rollup_skip on|off. Deliberately carries no secrets and no per-user data.")
	// Public client-config advertisement (boom-93f.1.1): auth provider,
	// registration/billing/beta switches the FE needs at boot. Non-sensitive,
	// same public audience as /version + /healthz.
	apiroute.GET(e, "/api/v1/config/public", h.PublicConfig).
		Doc("Public client bootstrap config",
			"The non-sensitive server flags the frontend must know BEFORE anyone logs in, so it "+
				"can pick the right signup/login flow instead of discovering the answer by "+
				"POSTing to /auth/register and catching a 403. Keys are snake_case: "+
				"registration_enabled, auth_provider (\"local\" or \"oidc\"), oidc_enabled (a "+
				"convenience derivation of auth_provider), billing_enabled, "+
				"github_connect_enabled (true only when the gate is on AND the OAuth-App "+
				"credentials are provisioned), books_enabled, and beta_flags — a map of "+
				"server-side kill switches (currently user_registration) the FE checks before "+
				"honoring the matching ?enable_beta_* URL flag. Reads config only, never the "+
				"database, so it cannot degrade. Booleans and a provider name only: no secrets, "+
				"no admin-only flags, no per-user data.")
	openapi.Register(e)

	// Server-log stream cluster (boom-awh.2): REST tail (Authorization-gated)
	// and the live WebSocket (refresh_token-cookie-gated because WS handshakes
	// can't carry an Authorization header).
	apiroute.GET(e, "/api/v1/logs", h.ServerLogs).
		Doc("Server log tail (polling)",
			"Polling fallback for the boomtime server's OWN log records, read from an in-memory "+
				"ring buffer (the last ~10k entries) — nothing is read from disk or the "+
				"database, and entries are lost on restart. Requires the Authorization header. "+
				"Pass ?afterId=<id> to resume: only entries whose monotonic id is greater than "+
				"afterId are returned, and the default of 0 returns the whole retained buffer. "+
				"Owner scoping is enforced server-side — records tagged with a different owner "+
				"are dropped before the response is built, while untagged server-scope records "+
				"(startup, migrations, health) fan out to every authenticated caller. \"logs\" "+
				"is always an array, never null. Use /api/v1/logs/ws for the live stream.")
	apiroute.WebSocket(e, "/api/v1/logs/ws", h.ServerLogsWS).
		Doc("Server log live stream",
			"WebSocket upgrade — answers 101 Switching Protocols with no HTTP body, so Swagger "+
				"UI cannot exercise it. Carries the same records as GET /api/v1/logs. "+
				"Authentication is the HttpOnly refresh_token COOKIE, not the Authorization "+
				"header: a WebSocket handshake cannot set headers, and a query-param access "+
				"token would leak into proxy and server access logs. On connect the server "+
				"writes one {\"type\":\"snapshot\",\"logs\":[...]} frame backfilled from "+
				"?afterId=<id>, then one {\"type\":\"log\",\"log\":{...}} frame per live record; "+
				"ids are monotonic so a client can de-duplicate the overlap and resume after a "+
				"reload. Both the snapshot and every live frame pass through the same "+
				"owner filter as the REST tail, so the two paths cannot skew apart.")
}
