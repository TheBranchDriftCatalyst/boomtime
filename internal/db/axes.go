// axes.go is the canonical axis registry. Every curation/dashboard label axis is
// declared ONCE here; the ordered hiddenAxes list and the per-query column maps
// (raw scan, rollup fast path, projects-list join, explore whitelist) are derived
// from it. Add or change an axis by editing one axisDef row — the derived values
// are pinned by TestAxisRegistryDerivations.
package db

// axisDef declares one label axis: its FE-facing/curation name, its column on the
// raw heartbeats table, and whether the pre-aggregated hb_rollup_daily table
// stores it (which drives the rollup fast path vs raw-scan fallback).
type axisDef struct {
	name     string
	rawCol   string
	inRollup bool
}

// axes is the registry, in the deterministic order the exclusion/inclusion
// predicates are built in (ordered for stable SQL/arg building).
//
// inRollup marks the axes stored on hb_rollup_daily: project/language/editor/
// platform/machine/category/plugin/branch. Entity is intentionally excluded
// (per-file, near raw cardinality — storing it would defeat the rollup).
var axes = []axisDef{
	{name: "project", rawCol: "project", inRollup: true},
	{name: "language", rawCol: "language", inRollup: true},
	{name: "editor", rawCol: "editor", inRollup: true},
	{name: "plugin", rawCol: "plugin", inRollup: true},
	{name: "machine", rawCol: "machine", inRollup: true},
	{name: "platform", rawCol: "platform", inRollup: true},
	{name: "branch", rawCol: "branch", inRollup: true},
	{name: "category", rawCol: "category", inRollup: true},
}

// hiddenAxes is the definitive set of curation-hide axes excluded from the
// aggregate dashboards. Suppressing a value on any of these axes removes it from
// stats/projects/big-bet dashboards. Ordered for deterministic SQL/arg building.
var hiddenAxes = func() []string {
	out := make([]string, len(axes))
	for i, a := range axes {
		out[i] = a.name
	}
	return out
}()

// rawHeartbeatCols maps every hidden axis to its column on the raw heartbeats
// table. Used by all queries whose innermost scan is `heartbeats` (all axes are
// available). `type` is stored in the ty column but is not a hide axis here.
var rawHeartbeatCols = func() map[string]string {
	m := make(map[string]string, len(axes))
	for _, a := range axes {
		m[a.name] = a.rawCol
	}
	return m
}()

// RollupAxes are the hide/scope axes the pre-aggregated hb_rollup_daily table
// can exclude or include (it stores these columns). Today that's every axis
// except entity, so only an entity hide (impossible — not a hiddenAxis) or an
// entity Space rule forces the raw path — see HasHiddenOutside/HasMemberOutside;
// the stats handler falls back accordingly.
var RollupAxes = func() map[string]bool {
	m := map[string]bool{}
	for _, a := range axes {
		if a.inRollup {
			m[a.name] = true
		}
	}
	return m
}()

// rollupCols maps the rollup-available hide axes to their columns.
var rollupCols = func() map[string]string {
	m := map[string]string{}
	for _, a := range axes {
		if a.inRollup {
			m[a.name] = a.rawCol
		}
	}
	return m
}()

// projectListCols maps hide axes to their heartbeats-qualified columns for the
// projects-list join. A project only surfaces if it has heartbeats not matching
// any hidden value, so a project consisting solely of hidden activity disappears.
var projectListCols = func() map[string]string {
	m := make(map[string]string, len(axes))
	for _, a := range axes {
		m[a.name] = "heartbeats." + a.rawCol
	}
	return m
}()
