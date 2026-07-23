// scrub.go: the unified "public-safe" widget-payload scrubber (bd gaka-6jm.3).
//
// # Public-safe contract
//
// Every response served by the public widget/badge endpoints MUST be run
// through Scrub before it leaves the process. Scrub enforces the following
// invariants on the returned *model.StatsPayload:
//
//  1. NO file paths (aka "entities") are ever exposed. The StatsPayload shape
//     defined in internal/model/stats.go carries only aggregated per-resource
//     rows (Projects, Languages, Editors, Platforms, Machines, Categories) —
//     it has no Entity field. This is a REGRESSION GUARD: if a future
//     StatsPayload change adds a per-file field, scrub_test.go's compile-time
//     assertion will break, forcing a review of whether that field is safe to
//     expose on public embeds.
//
//  2. NO branch names are ever exposed. Same rationale as (1): StatsPayload
//     has no Branches field on the widget path. (Branches DO appear on the
//     authenticated per-project detail response — a different type — which
//     is unreachable from these public endpoints.)
//
//  3. NO raw machine identifiers beyond the curated Machines segment. The
//     Machines segment is subject to hide rules like every other axis, so a
//     hidden machine name never appears in the top-N rows OR in the
//     "Other (N more)" tail (see contract 4).
//
//  4. Hidden values on curated axes (project / language / editor / platform /
//     machine / category) never appear in the returned payload — neither in
//     the top-N rows (which the DB queries already exclude via
//     exclusionPredicate; see internal/db/predicates.go) NOR in the
//     OtherMembers tail on the synthesized "Other (N more)" bucket (which
//     capWithOther collapses in application code AFTER the SQL predicates
//     have run, and so is the specific gap this scrubber closes).
//
// # Where the tail leak came from
//
// internal/stats/segment.go's capWithOther collapses the long tail of a
// segment into a single "Other (N more)" ResourceStats and, for FE tooltip
// support, carries the top otherMembersCap tail members as OtherMembers. On
// the AUTHENTICATED dashboard endpoints this is fine: the DB has already
// excluded hidden rows, so the tail is drawn from the caller's already-
// curated pool. On the PUBLIC widget endpoint, the same code path runs — and
// the hidden values are excluded from top-N and from the underlying scan —
// but if a hide rule was authored AFTER a widget snapshot was cached, or if
// there is any drift between the DB-side hide set and the render pipeline,
// the tail is the exact place a leak would land. Scrub is the belt to the
// SQL predicate's braces.
//
// # What Scrub does NOT do
//
// - Scrub does NOT modify the input payload. It returns a *StatsPayload
//   that shares memory with the input wherever no filter fired.
//
// - Scrub does NOT rename anything. Rename rules are applied upstream at
//   query time (RenameSets in internal/db); by the time a value reaches this
//   scrubber it is already presented under its rename target and matches
//   hide rules on that target name.
//
// - Scrub does NOT apply the badge-endpoint's project-level 404. That is a
//   separate policy handled inline by applyBadgeCuration in
//   internal/handler/badges.go, because a badge whose sole subject is a
//   hidden project has no "scrubbed" representation — it must 404.
package widget

import (
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// Scrub applies the public-safe contract documented at the top of this file to
// a widget payload. Returns a *StatsPayload safe to serialize into a public
// SVG response.
//
// If payload is nil or hidden has no relevant values, Scrub returns the input
// pointer unchanged (a common no-op fast path — most owners have no hide
// rules).
//
// Scrub is idempotent: Scrub(Scrub(p, h), h) == Scrub(p, h).
func Scrub(payload *model.StatsPayload, hidden model.HiddenSets) *model.StatsPayload {
	if payload == nil {
		return nil
	}
	// The tail-scrub is currently the only piece of application-side filtering
	// needed to enforce the public-safe contract, because StatsPayload does
	// not carry any file-path / branch / raw-heartbeat fields in the first
	// place (see contract 1-3 in the file docstring — enforced at the
	// StatsPayload TYPE level, verified by scrub_test.go).
	return payload.ScrubTail(hidden)
}

// ScrubMomentum enforces the public-safe contract on the momentum widget's
// payload (bd gaka-6jm.6). MomentumPayload carries per-project rows keyed by
// project name; the DB predicate (db.GetMomentum) already excludes hidden
// projects at query time, but this scrubber is the belt to that brace — if a
// hide rule was authored AFTER a widget snapshot was cached, or if there is any
// drift between the DB-side hide set and the render pipeline, a hidden project
// name would land here. Idempotent; returns the input pointer unchanged when
// there is nothing to filter.
//
// Punchcard and Sessions payloads are NOT scrubbed here because their public
// shapes carry no project / language / editor / machine identifiers:
//
//   - PunchcardPayload is a pure dow×hour intensity grid (Cells is
//     []{Dow, Hour, Seconds}). Its aggregate is already subject to the owner's
//     hide rules via db.GetPunchcard's exclusionPredicate. No labels leak.
//
//   - SessionsPayload is Summary (counts + durations) + per-DATE Daily +
//     duration Histogram bins ("<15m", "15-30m", …). No project / axis
//     identifiers; the daily series is keyed by ISO date, not by any curated
//     axis value.
//
// If MomentumPayload gains a per-file/branch/entity field in future, extend
// this function (and add a scrub_test case). Contract clauses 1-3 of scrub.go
// still apply to any downstream renderer.
func ScrubMomentum(payload *model.MomentumPayload, hidden model.HiddenSets) *model.MomentumPayload {
	if payload == nil || hidden == nil {
		return payload
	}
	projs := hidden.Values("project")
	if len(projs) == 0 {
		return payload
	}
	hiddenSet := make(map[string]struct{}, len(projs))
	for _, v := range projs {
		hiddenSet[strings.ToLower(v)] = struct{}{}
	}
	// Fast path: if no project name in the payload matches a hidden value, we
	// can return the input pointer unchanged — the common case for owners with
	// hide rules that don't intersect the top-N momentum rows.
	filteredIdxs := make([]int, 0, len(payload.Projects))
	for i, p := range payload.Projects {
		if _, hit := hiddenSet[strings.ToLower(p.Name)]; hit {
			continue
		}
		filteredIdxs = append(filteredIdxs, i)
	}
	if len(filteredIdxs) == len(payload.Projects) {
		return payload
	}
	out := *payload
	out.Projects = make([]model.MomentumProject, 0, len(filteredIdxs))
	for _, i := range filteredIdxs {
		out.Projects = append(out.Projects, payload.Projects[i])
	}
	return &out
}
