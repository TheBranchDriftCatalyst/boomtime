// books_diagnostics.go: an admin-only "dump everything this source returns" tool
// (gaka-books). It runs raw signed PROBES against the Audible + Kindle endpoints
// using the admin's own Amazon device credential and returns each response
// verbatim (status + parsed JSON or raw text), so we can eyeball every field a
// source exposes before committing it to the reading_items model. This is also
// how the reverse-engineered request signing gets verified against each host.
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/labstack/echo/v5"
)

type diagProbe struct {
	Name     string          `json:"name"`
	Endpoint string          `json:"endpoint"`
	Status   int             `json:"status"`
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`     // parsed JSON when valid
	BodyText string          `json:"bodyText,omitempty"` // raw text (XML / error pages)
}

// runProbe signs + GETs one endpoint and captures the response verbatim.
func runProbe(ctx context.Context, cred *amazon.DeviceCredential, name, host, path string) diagProbe {
	p := diagProbe{Name: name, Endpoint: "https://" + host + path}
	body, status, err := amazon.SignedGet(ctx, cred, host, path)
	p.Status = status
	if err != nil {
		p.Error = err.Error()
		return p
	}
	p.OK = status >= 200 && status < 300
	if json.Valid(body) {
		p.Body = json.RawMessage(body)
	} else {
		s := string(body)
		if len(s) > 20000 {
			s = s[:20000] + "…(truncated)"
		}
		p.BodyText = s
	}
	return p
}

// AdminBooksDiagnostics: GET /api/v1/admin/books/diagnostics?source=audible|kindle
// Admin-only. Loads the caller's Amazon credential and probes the source's raw
// endpoints so we can inventory every available metric/field.
func (h *Handler) AdminBooksDiagnostics(c *echo.Context) error {
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	ctx := c.Request().Context()
	cred, err := amazon.NewStore(h.DB).Load(ctx, owner)
	if err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("no Amazon credential — connect Amazon in Settings first ("+err.Error()+")"))
	}

	var probes []diagProbe
	switch c.QueryParam("source") {
	case "kindle":
		probes = kindleProbes(ctx, cred)
	default: // audible
		probes = audibleProbes(ctx, cred)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"source":      firstNonEmpty(c.QueryParam("source"), "audible"),
		"marketplace": cred.Marketplace,
		"probes":      probes,
	})
}

// audibleProbes hits the Audible API with the WIDEST response_groups so every
// field shows up, plus the listening-stats endpoints.
func audibleProbes(ctx context.Context, cred *amazon.DeviceCredential) []diagProbe {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	groups := strings.Join([]string{
		"product_desc", "product_extended_attrs", "product_attrs", "product_details",
		"contributors", "series", "categories", "rating", "reviews", "review_attrs",
		"is_finished", "percent_complete", "listening_status", "media", "sample",
		"price", "relationships", "origin_asin", "provided_review", "customer_rights",
		"is_downloaded", "is_returnable", "is_removable", "order_details",
	}, ",")
	return []diagProbe{
		runProbe(ctx, cred, "library (all response_groups)", host,
			"/1.0/library?response_groups="+groups+"&num_results=25&page=1"),
		runProbe(ctx, cred, "stats/aggregates (listening time)", host,
			"/1.0/stats/aggregates?response_groups=total_listening_stats&store=Audible"),
	}
}

// kindleProbes hits the reverse-engineered Kindle endpoints (library metadata +
// whispersync). These are less battle-tested than Audible — the diagnostic
// surfaces exactly what each returns (incl. errors) so we can iterate live.
func kindleProbes(ctx context.Context, cred *amazon.DeviceCredential) []diagProbe {
	probes := []diagProbe{
		runProbe(ctx, cred, "Fiona library metadata (XML)", "todo-ta-g7g.amazon.com",
			"/FionaTodoListProxy/syncMetaData?type=EBOK"),
	}
	if cred.CustomerID != "" {
		probes = append(probes, runProbe(ctx, cred, "whispersync datasets", "api.amazon.com",
			"/whispersync/v2/data/"+cred.CustomerID+"/datasets"))
	} else {
		probes = append(probes, diagProbe{
			Name:  "whispersync datasets",
			Error: "no customer_id on the stored credential — reconnect Amazon to capture it",
		})
	}
	return probes
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
