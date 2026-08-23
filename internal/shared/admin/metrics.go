package admin

import (
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v5"
	dto "github.com/prometheus/client_model/go"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// AdminMetrics returns a JSON view of the Prometheus registry (boom-metrics)
// for the admin Metrics tab. It Gather()s internal/metrics.Registry — the SAME
// registry served at /metrics for the cluster scrape — and flattens it into a
// small, FE-friendly shape: one family per metric, each with its samples
// (label set + value). This is the in-app "keep a view" surface; Grafana is the
// heavyweight one over the /metrics scrape.
//
// Admin-gated (requireAdmin runs before any work). Read-only: Gather() never
// mutates the registry. The registry is process-global in-memory, so in a
// multi-pod deployment this reflects the pod that served the request — fine for
// the operator "what is this pod doing right now" view the tab is for; the
// authoritative cross-pod picture is Prometheus/Grafana.
//
// Query params:
//
//   - names=<a,b,c>  return only families whose name has one of these comma-
//     separated PREFIXES (e.g. names=http_,jobs_). Empty = all families.
//
// Response: {"families": [ {name, help, type, samples:[{labels, value|count|sum}]} ]}.
func (h *Handler) AdminMetrics(c *echo.Context) error {
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	families, err := metrics.Registry.Gather()
	if err != nil {
		// Gather only errors on a malformed collector (a programming bug); surface
		// it rather than silently returning a partial view.
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	prefixes := splitCSV(c.QueryParam("names"))

	out := make([]metricFamilyView, 0, len(families))
	for _, mf := range families {
		name := mf.GetName()
		if len(prefixes) > 0 && !hasAnyPrefix(name, prefixes) {
			continue
		}
		out = append(out, toFamilyView(mf))
	}
	// Gather already returns families sorted by name, but pin it so the FE order
	// is stable regardless of the filter.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return c.JSON(http.StatusOK, map[string]any{"families": out})
}

// metricFamilyView is one metric family flattened for the FE: its name, help,
// type ("counter" | "gauge" | "histogram" | "summary" | "untyped"), and the
// per-label-set samples.
type metricFamilyView struct {
	Name    string         `json:"name"`
	Help    string         `json:"help,omitempty"`
	Type    string         `json:"type"`
	Samples []metricSample `json:"samples"`
}

// metricSample is one label-set of a family. For counters/gauges only Value is
// set; for histograms/summaries Count + Sum are set (the FE derives an average
// = Sum/Count for a latency read-out) and Value is left nil.
type metricSample struct {
	Labels map[string]string `json:"labels,omitempty"`
	Value  *float64          `json:"value,omitempty"`
	Count  *uint64           `json:"count,omitempty"`
	Sum    *float64          `json:"sum,omitempty"`
}

func toFamilyView(mf *dto.MetricFamily) metricFamilyView {
	fv := metricFamilyView{
		Name:    mf.GetName(),
		Help:    mf.GetHelp(),
		Type:    strings.ToLower(mf.GetType().String()),
		Samples: make([]metricSample, 0, len(mf.GetMetric())),
	}
	for _, m := range mf.GetMetric() {
		s := metricSample{Labels: labelsToMap(m.GetLabel())}
		switch {
		case m.GetCounter() != nil:
			v := m.GetCounter().GetValue()
			s.Value = &v
		case m.GetGauge() != nil:
			v := m.GetGauge().GetValue()
			s.Value = &v
		case m.GetHistogram() != nil:
			cnt := m.GetHistogram().GetSampleCount()
			sum := m.GetHistogram().GetSampleSum()
			s.Count = &cnt
			s.Sum = &sum
		case m.GetSummary() != nil:
			cnt := m.GetSummary().GetSampleCount()
			sum := m.GetSummary().GetSampleSum()
			s.Count = &cnt
			s.Sum = &sum
		case m.GetUntyped() != nil:
			v := m.GetUntyped().GetValue()
			s.Value = &v
		}
		fv.Samples = append(fv.Samples, s)
	}
	return fv
}

func labelsToMap(pairs []*dto.LabelPair) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		out[p.GetName()] = p.GetValue()
	}
	return out
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

// hasAnyPrefix reports whether name starts with any of the given prefixes.
func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
