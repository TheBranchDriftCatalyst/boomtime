// books_diagnostics.go: an admin-only "dump everything this source returns" tool
// (boom-books). It runs raw signed PROBES against the Audible + Kindle endpoints
// using the admin's own Amazon device credential and returns each response
// verbatim (status + parsed JSON or raw text), so we can eyeball every field a
// source exposes before committing it to the reading_items model. This is also
// how the reverse-engineered request signing gets verified against each host.
package admin

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
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

	// Verdict + Detail are set only by the LIBERATION source (boom-w20s.19).
	// The Audible/Kindle probes are raw response dumps — "here is what came
	// back, eyeball it" — whereas liberation probes answer a specific yes/no
	// question about a protocol assumption, so they carry an explicit judgement
	// and a human-readable explanation. Empty on the dump-style probes, which is
	// why both are omitempty and why the FE renders them conditionally.
	Verdict string `json:"verdict,omitempty"` // pass | warn | fail | skip
	Detail  string `json:"detail,omitempty"`
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

// diagnosticsResponse is GET /api/v1/admin/books/diagnostics.
//
// This was a map[string]any literal. Naming it is not cosmetic: a map has no
// shape to reflect, so the OpenAPI spec could only ever emit a bare object for
// it. Every json tag below is the map key it replaced, character for character,
// and asin keeps omitempty because the map added that key only when the
// liberation probe actually resolved a title.
type diagnosticsResponse struct {
	// Source echoes the ?source= that ran, defaulted to "audible".
	Source string `json:"source"`
	// Marketplace is the Amazon marketplace the caller's credential belongs to.
	Marketplace string `json:"marketplace"`
	// Probes is one entry per endpoint probed, in the order they ran.
	Probes []diagProbe `json:"probes"`
	// ASIN is set by the liberation source only, naming the title it verified.
	ASIN string `json:"asin,omitempty"`
}

// AdminBooksDiagnostics: GET /api/v1/admin/books/diagnostics?source=audible|kindle
// Admin-only. Loads the caller's Amazon credential and probes the source's raw
// endpoints so we can inventory every available metric/field.
func (h *Handler) AdminBooksDiagnostics(c *echo.Context) (diagnosticsResponse, error) {
	var out diagnosticsResponse
	owner, aerr := h.requireAdmin(c)
	if aerr != nil {
		return out, aerr
	}
	ctx := c.Request().Context()
	cred, err := amazon.NewStore(h.DB).Load(ctx, owner)
	if err != nil {
		return out, apierr.BadRequest("no Amazon credential — connect Amazon in Settings first (" + err.Error() + ")")
	}

	var (
		probes []diagProbe
		report liberate.Report
	)
	switch c.QueryParam("source") {
	case "kindle":
		probes = kindleProbes(ctx, cred)
	case "liberation":
		// boom-w20s.19. Verifies the liberation protocol assumptions live and
		// reports which voucher key derivation actually works. Optionally scoped
		// to one title via ?asin=; otherwise it picks the first library item.
		report = liberate.RunProbes(ctx, cred, strings.TrimSpace(c.QueryParam("asin")))
		probes = liberationProbes(report)
	default: // audible
		probes = audibleProbes(ctx, cred)
	}
	resp := diagnosticsResponse{
		Source:      firstNonEmpty(c.QueryParam("source"), "audible"),
		Marketplace: string(cred.Marketplace),
		Probes:      probes,
	}
	if report.ASIN != "" {
		resp.ASIN = report.ASIN
	}
	return resp, nil
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

// liberationProbes adapts the liberate package's verification results onto the
// shared diagProbe shape, so the liberation sweep renders through the SAME admin
// UI component as the Audible/Kindle dumps instead of needing its own panel.
//
// liberate.Report is deliberately not reused as the wire type: the admin surface
// owns its response contract, and keeping the adapter here means the liberate
// package has no opinion about HTTP.
func liberationProbes(r liberate.Report) []diagProbe {
	out := make([]diagProbe, 0, len(r.Probes))
	for _, p := range r.Probes {
		out = append(out, diagProbe{
			Name:     p.Name,
			Endpoint: p.Endpoint,
			Status:   p.Status,
			OK:       p.OK,
			Error:    p.Error,
			Body:     p.Body,
			BodyText: p.BodyText,
			Verdict:  string(p.Verdict),
			Detail:   p.Detail,
		})
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
