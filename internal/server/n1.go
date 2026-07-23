package server

import (
	"log/slog"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// n1Middleware stashes a per-request DB query counter in the request context so
// the pgx n1Tracer (internal/db) can increment it, then logs a WARN at request
// end when the request issued more than n1Threshold queries or ran the same
// normalized statement more than dupThreshold times (classic N+1 in a loop).
//
// A zero threshold disables that particular check. When both are zero the
// middleware still installs the counter (harmless, O(1) per query) but never
// warns.
func n1Middleware(logger *slog.Logger, n1Threshold, dupThreshold int) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			ctx := db.WithReqStats(req.Context())
			c.SetRequest(req.WithContext(ctx))

			err := next(c)

			total, maxDup, dupSQL, ok := db.ReqStatsSummary(ctx)
			if !ok {
				return err
			}
			overCount := n1Threshold > 0 && total > n1Threshold
			overDup := dupThreshold > 0 && maxDup > dupThreshold
			if overCount || overDup {
				logger.Warn("db N+1 suspected",
					"method", req.Method,
					"path", req.URL.Path,
					"queries", total,
					"max_duplicate", maxDup,
					"duplicate_sql", dupSQL,
				)
			}
			return err
		}
	}
}

// userCtxMiddleware resolves the request's bearer token to an owner and stashes
// that owner into the request context via db.WithUser BEFORE any handler runs.
// The pgx tracer (internal/db/observability.go) then reads db.UserFrom(ctx) and
// emits it as slog attr "user" on every query event; logging.FilterForUser (in
// internal/logging) drops those events for cross-tenant Logs viewers.
//
// Fixes gaka-ar7: without this hook, the DB tracer's DEBUG SQL narration (e.g.
// "UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1") fans out
// to every authenticated Logs viewer because the record carries no owner attr,
// leaking activity metadata cross-tenant even though bind args are already
// redacted.
//
// This middleware is fail-open: any error resolving the token (missing header,
// unknown token, DB error) leaves ctx unchanged. The downstream handler still
// runs — its own resolveUser call is what decides whether the request is
// authorized. All this middleware controls is whether the tracer's follow-on
// DEBUG events are tagged with an owner; leaving them untagged on failure is
// exactly the server-scope semantics FilterForUser already handles.
//
// Installed BEFORE handler registration (server.New) so it wraps every route.
// Public/unauth'd routes (healthz, /widget/svg/*, /api/public/*) have no
// Authorization header → the token lookup no-ops → ctx unchanged → tracer emits
// no "user" attr → those DEBUG events remain visible to every viewer as
// server-scope.
func userCtxMiddleware(database *db.DB) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			tkn, ok := auth.ParseAuthHeader(req.Header.Get(echo.HeaderAuthorization))
			if !ok || tkn == "" {
				return next(c)
			}
			owner, ok, err := database.GetUserByToken(req.Context(), tkn)
			if err != nil || !ok || owner == "" {
				return next(c)
			}
			c.SetRequest(req.WithContext(db.WithUser(req.Context(), owner)))
			return next(c)
		}
	}
}
