package server

import (
	"log/slog"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/db"
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
