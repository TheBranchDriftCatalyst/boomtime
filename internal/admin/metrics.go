package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// AdminMetrics returns the in-memory rate-metric registry snapshot for the
// admin Metrics dashboard (gaka-metrics). Every instrumented series — router
// request rates, the per-kind job rate-limiter, and external-API call rates —
// is returned with its rolling per-minute points.
//
// Admin-gated (requireAdmin runs before any work). Read-only: it never mutates
// the registry. The registry is process-global in-memory, so in a multi-pod
// deployment this reflects the pod that served the request; that is fine for
// the operator "is this saturating right now" view the dashboard is for.
//
// Query params:
//
//   - since=<RFC3339>  drop points before this instant (default: the full ~2h
//     window). Bad values are ignored (treated as unset) rather than erroring.
//   - names=<a,b,c>    return only series whose name has one of these comma-
//     separated PREFIXES (e.g. names=http.,jobs.). Empty = all series.
//
// Response: {"series": [ {name, kind, unit?, points:[{bucket,value}]} ... ]}.
func (h *Handler) AdminMetrics(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	var since time.Time
	if raw := c.QueryParam("since"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t
		}
	}

	series := metrics.Snapshot(since)

	if prefixes := splitCSV(c.QueryParam("names")); len(prefixes) > 0 {
		series = filterByPrefix(series, prefixes)
	}

	return c.JSON(http.StatusOK, map[string]any{"series": series})
}

// splitCSV splits a comma-separated query value into trimmed, non-empty tokens.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, tok := range strings.Split(s, ",") {
		if tok = strings.TrimSpace(tok); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// filterByPrefix keeps series whose name starts with any of the given prefixes.
func filterByPrefix(series []metrics.Series, prefixes []string) []metrics.Series {
	out := make([]metrics.Series, 0, len(series))
	for _, s := range series {
		for _, p := range prefixes {
			if strings.HasPrefix(s.Name, p) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}
