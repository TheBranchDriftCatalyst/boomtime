package ingest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// maxDistinctInSummary caps how many distinct project/language/editor values a
// single sampled line lists before collapsing the rest into a "+N" suffix, so
// one narration line stays readable even for a big mixed batch.
const maxDistinctInSummary = 6

// logIngestSampled emits ONE "heartbeats ingested" narration line per
// Cfg.HeartbeatLogSampleN heartbeats ingested process-wide — enough to prove
// ingest is alive + flowing (with a batch summary) WITHOUT a per-heartbeat or
// per-request flood on this hot path.
//
// Sampling is by heartbeat COUNT, not request: the cumulative counter advances
// by the batch size and a line is emitted only when the batch crosses an
// N-boundary, so the 1:N rate holds regardless of how heartbeats are batched.
// N<=0 (or an empty batch) disables it. Lock-free — only an atomic add on the
// common path; the batch summary is computed on the rare sampled call.
func (h *Handler) logIngestSampled(owner string, hbs []model.HeartbeatPayload) {
	n := h.Cfg.HeartbeatLogSampleN
	if n <= 0 || len(hbs) == 0 {
		return
	}
	before := h.hbIngested.Load()
	after := h.hbIngested.Add(int64(len(hbs)))
	// Emit only when this batch pushed the running total across a multiple of N.
	if before/int64(n) == after/int64(n) {
		return
	}
	projects, languages, editors := summarizeBatch(hbs)
	h.Logger.Info("heartbeats ingested",
		"sampled", fmt.Sprintf("1:%d", n),
		"total", after,
		"batch", len(hbs),
		"owner", owner,
		"projects", projects,
		"languages", languages,
		"editors", editors,
	)
}

// summarizeBatch returns the distinct project / language / editor values seen in
// the batch (each capped + "+N" suffixed) so the sampled line carries a real
// sense of WHAT was ingested, not just a count.
func summarizeBatch(hbs []model.HeartbeatPayload) (projects, languages, editors string) {
	return distinctPtr(hbs, func(hb model.HeartbeatPayload) *string { return hb.Project }),
		distinctPtr(hbs, func(hb model.HeartbeatPayload) *string { return hb.Language }),
		distinctPtr(hbs, func(hb model.HeartbeatPayload) *string { return hb.Editor })
}

// distinctPtr collects the distinct non-empty values of one *string field across
// the batch, sorts them for a stable line, caps at maxDistinctInSummary, and
// appends " +N" when more were elided. Returns "" when the field was never set.
func distinctPtr(hbs []model.HeartbeatPayload, get func(model.HeartbeatPayload) *string) string {
	seen := make(map[string]struct{})
	var vals []string
	for _, hb := range hbs {
		p := get(hb)
		if p == nil || *p == "" {
			continue
		}
		if _, ok := seen[*p]; ok {
			continue
		}
		seen[*p] = struct{}{}
		vals = append(vals, *p)
	}
	sort.Strings(vals)
	extra := 0
	if len(vals) > maxDistinctInSummary {
		extra = len(vals) - maxDistinctInSummary
		vals = vals[:maxDistinctInSummary]
	}
	out := strings.Join(vals, ",")
	if extra > 0 {
		out += fmt.Sprintf(" +%d", extra)
	}
	return out
}
