// scrub_test.go covers the unified public-safe scrubber (bd gaka-6jm.3).
//
// The tests here are the executable form of the "public-safe contract"
// documented at the top of scrub.go. Every assertion maps to one clause of
// that contract:
//
//   TestScrub_HiddenProjectNeverAppears           -> contract clause 4 (project axis)
//   TestScrub_HiddenLanguageNeverAppears          -> contract clause 4 (language axis, incl. tail)
//   TestScrub_StatsPayloadNeverExposesFilePaths   -> contract clauses 1-3 (regression guard)
//   TestScrub_Idempotent                          -> "Scrub is idempotent" property
//   TestScrub_NilAndEmpty                         -> nil-guard fast path
package widget

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// mkPayload builds a payload with predictable top-N + OtherMembers content so
// each hidden-axis test can assert both slots.
func mkPayload() *model.StatsPayload {
	return &model.StatsPayload{
		Projects: []model.ResourceStats{
			{Name: "public-a", TotalSeconds: 1000},
			{Name: "Other (3 more)",
				TotalSeconds: 30,
				OtherCount:   3,
				OtherMembers: []model.OtherMember{
					{Name: "hakatime", TotalSeconds: 20},
					{Name: "shown-b", TotalSeconds: 7},
					{Name: "shown-c", TotalSeconds: 3},
				},
			},
		},
		Languages: []model.ResourceStats{
			{Name: "Go", TotalSeconds: 900},
			{Name: "Other (2 more)",
				TotalSeconds: 50,
				OtherCount:   2,
				OtherMembers: []model.OtherMember{
					{Name: "Haskell", TotalSeconds: 30},
					{Name: "Rust", TotalSeconds: 20},
				},
			},
		},
		Editors: []model.ResourceStats{
			{Name: "vscode", TotalSeconds: 100},
		},
		Machines: []model.ResourceStats{
			{Name: "laptop", TotalSeconds: 100},
			{Name: "Other (1 more)",
				OtherCount: 1,
				OtherMembers: []model.OtherMember{
					{Name: "SECRET-BOX", TotalSeconds: 5},
				},
			},
		},
	}
}

// containsName scans EVERY string field of the payload (top-N Name and
// OtherMembers[].Name) for a match. Case-insensitive so a hide rule on
// "hakatime" catches "Hakatime" if it slipped through anywhere.
func containsName(p *model.StatsPayload, needle string) bool {
	needle = strings.ToLower(needle)
	segs := [][]model.ResourceStats{
		p.Projects, p.Languages, p.Editors, p.Platforms, p.Machines, p.Categories,
	}
	for _, seg := range segs {
		for _, r := range seg {
			if strings.ToLower(r.Name) == needle {
				return true
			}
			for _, m := range r.OtherMembers {
				if strings.ToLower(m.Name) == needle {
					return true
				}
			}
		}
	}
	return false
}

// TestScrub_HiddenProjectNeverAppears: a hidden project name must not appear
// anywhere in the scrubbed payload. This test represents the case where the
// DB predicate already dropped a hidden project from top-N (we simulate that
// by only placing "hakatime" in the tail) but the OtherMembers tooltip payload
// would still leak it. Scrub must strip it.
func TestScrub_HiddenProjectNeverAppears(t *testing.T) {
	p := mkPayload()
	if !containsName(p, "hakatime") {
		t.Fatalf("fixture bug: hakatime should be in the input payload's project tail")
	}
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	got := Scrub(p, hidden)
	if containsName(got, "hakatime") {
		t.Errorf("hidden project 'hakatime' leaked in scrubbed payload: %+v", got.Projects)
	}
	// Non-hidden tail members must survive.
	if !containsName(got, "shown-b") || !containsName(got, "shown-c") {
		t.Errorf("scrub over-filtered: dropped non-hidden tail members")
	}
	// The input payload MUST NOT be mutated.
	if !containsName(p, "hakatime") {
		t.Errorf("Scrub mutated the input payload — must return a copy for filtered rows")
	}
}

// TestScrub_HiddenLanguageNeverAppears: same as above for the language axis.
// Also asserts that a hide rule on one axis does NOT touch other axes' tails.
func TestScrub_HiddenLanguageNeverAppears(t *testing.T) {
	p := mkPayload()
	hidden := model.HiddenSetsMap{"language": {"haskell"}}
	got := Scrub(p, hidden)
	if containsName(got, "Haskell") {
		t.Errorf("hidden language 'Haskell' leaked: %+v", got.Languages)
	}
	// Sibling axis tails must remain untouched — a language hide MUST NOT drop a
	// project tail entry that happens to share a name.
	if !containsName(got, "hakatime") {
		t.Errorf("Scrub cross-contaminated axes: language-hide dropped project tail entry")
	}
	// A non-hidden language ("Rust") in the same tail must survive.
	if !containsName(got, "Rust") {
		t.Errorf("Scrub over-filtered languages: dropped non-hidden 'Rust'")
	}
}

// TestScrub_HiddenMachineNeverAppears: guards the "no raw machine identifiers"
// clause (contract 3). A curated machine must not appear in the tail.
func TestScrub_HiddenMachineNeverAppears(t *testing.T) {
	p := mkPayload()
	hidden := model.HiddenSetsMap{"machine": {"secret-box"}}
	got := Scrub(p, hidden)
	if containsName(got, "SECRET-BOX") {
		t.Errorf("hidden machine leaked in tail: %+v", got.Machines)
	}
}

// TestScrub_StatsPayloadNeverExposesFilePaths is the compile-time regression
// guard for contract clauses 1-3 in scrub.go: the widget StatsPayload MUST
// NOT gain a per-file, per-branch, or per-entity field. If someone adds one,
// this test breaks — forcing a public-safety review at the type level.
func TestScrub_StatsPayloadNeverExposesFilePaths(t *testing.T) {
	forbidden := map[string]struct{}{
		// per-file / raw-heartbeat fields — never safe on a public embed
		"Entity":   {},
		"Entities": {},
		"File":     {},
		"Files":    {},
		"Path":     {},
		"Paths":    {},
		// branch names — belong to authenticated project detail, never widgets
		"Branch":   {},
		"Branches": {},
	}
	rt := reflect.TypeOf(model.StatsPayload{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if _, bad := forbidden[f.Name]; bad {
			t.Errorf("StatsPayload gained forbidden public field %q — public widget contract violated. "+
				"If this field is intentional, update internal/widget/scrub.go's contract AND its Scrub "+
				"implementation to strip/curate it, then update this test.", f.Name)
		}
	}
}

// TestScrub_Idempotent: Scrub(Scrub(p, h), h) == Scrub(p, h).
func TestScrub_Idempotent(t *testing.T) {
	p := mkPayload()
	hidden := model.HiddenSetsMap{
		"project":  {"hakatime"},
		"language": {"haskell"},
	}
	once := Scrub(p, hidden)
	twice := Scrub(once, hidden)
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("Scrub not idempotent:\nonce=%+v\ntwice=%+v", once, twice)
	}
}

// TestScrubMomentum_HiddenProjectNeverAppears: MomentumPayload carries per-
// project rows keyed by project name (MomentumProject.Name). A hidden project
// must not appear in the scrubbed momentum payload — mirrors the tail scrub
// on the StatsPayload but applies to the top-level Projects slice, because
// MomentumPayload has no "Other" bucket to collapse into (bd gaka-6jm.6).
func TestScrubMomentum_HiddenProjectNeverAppears(t *testing.T) {
	mp := &model.MomentumPayload{
		Weeks: []string{"2026-01-05", "2026-01-12"},
		Projects: []model.MomentumProject{
			{Name: "public-a", Weekly: []int64{100, 200}, TotalSeconds: 300},
			{Name: "hakatime", Weekly: []int64{50, 50}, TotalSeconds: 100},
			{Name: "public-b", Weekly: []int64{10, 20}, TotalSeconds: 30},
		},
	}
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	got := ScrubMomentum(mp, hidden)
	for _, p := range got.Projects {
		if strings.EqualFold(p.Name, "hakatime") {
			t.Errorf("hidden project 'hakatime' leaked in momentum payload: %+v", got.Projects)
		}
	}
	// Non-hidden projects must survive.
	names := map[string]bool{}
	for _, p := range got.Projects {
		names[p.Name] = true
	}
	if !names["public-a"] || !names["public-b"] {
		t.Errorf("ScrubMomentum over-filtered: dropped visible projects; got %+v", got.Projects)
	}
	// Input MUST NOT be mutated — the original slice should still contain
	// hakatime so the caller (or another consumer) can trust the shared value.
	found := false
	for _, p := range mp.Projects {
		if p.Name == "hakatime" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ScrubMomentum mutated its input — must return a copy for filtered rows")
	}
	// Weeks axis is temporal and must be preserved verbatim.
	if len(got.Weeks) != len(mp.Weeks) {
		t.Errorf("ScrubMomentum altered Weeks axis: got %v, want %v", got.Weeks, mp.Weeks)
	}
}

// TestScrubMomentum_NoOpFastPaths exercises the return-input-unchanged cases.
func TestScrubMomentum_NoOpFastPaths(t *testing.T) {
	if got := ScrubMomentum(nil, model.HiddenSetsMap{"project": {"x"}}); got != nil {
		t.Errorf("ScrubMomentum(nil, h) = %+v, want nil", got)
	}
	mp := &model.MomentumPayload{
		Projects: []model.MomentumProject{{Name: "public-a", TotalSeconds: 100}},
	}
	if got := ScrubMomentum(mp, nil); got != mp {
		t.Errorf("ScrubMomentum(mp, nil) should return input pointer unchanged")
	}
	empty := model.HiddenSetsMap{"project": nil}
	if got := ScrubMomentum(mp, empty); got != mp {
		t.Errorf("ScrubMomentum(mp, empty) should return input pointer unchanged")
	}
	// No matching hidden values → return input pointer unchanged (fast path).
	hidden := model.HiddenSetsMap{"project": {"not-in-payload"}}
	if got := ScrubMomentum(mp, hidden); got != mp {
		t.Errorf("ScrubMomentum with no matches should return input pointer unchanged")
	}
}

// TestScrubMomentum_Idempotent: ScrubMomentum(ScrubMomentum(m, h), h) equals
// ScrubMomentum(m, h) — same idempotence property Scrub has.
func TestScrubMomentum_Idempotent(t *testing.T) {
	mp := &model.MomentumPayload{
		Weeks: []string{"2026-01-05"},
		Projects: []model.MomentumProject{
			{Name: "public-a", TotalSeconds: 100},
			{Name: "hakatime", TotalSeconds: 50},
		},
	}
	hidden := model.HiddenSetsMap{"project": {"hakatime"}}
	once := ScrubMomentum(mp, hidden)
	twice := ScrubMomentum(once, hidden)
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("ScrubMomentum not idempotent:\nonce=%+v\ntwice=%+v", once, twice)
	}
}

// TestPunchcardHasNoProjectLabels is a compile-time regression guard mirroring
// the StatsPayload guard above: the widget PunchcardPayload MUST remain a pure
// temporal (dow×hour) aggregate — no per-project / per-language / per-machine
// identifiers. If someone adds a Name or Project field to PunchcardCell (or a
// project-axis slice to PunchcardPayload), this test breaks, forcing a
// public-safety review AND a matching ScrubPunchcard implementation.
func TestPunchcardHasNoProjectLabels(t *testing.T) {
	forbidden := map[string]struct{}{
		"Project": {}, "Projects": {},
		"Language": {}, "Languages": {},
		"Machine": {}, "Machines": {},
		"Editor": {}, "Editors": {},
		"Name": {}, "Label": {},
	}
	for _, rt := range []reflect.Type{
		reflect.TypeOf(model.PunchcardPayload{}),
		reflect.TypeOf(model.PunchcardCell{}),
	} {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if _, bad := forbidden[f.Name]; bad {
				t.Errorf("%s gained forbidden public field %q — Punchcard is documented as pure temporal in scrub.go. "+
					"If this field is intentional, update ScrubMomentum's docstring, add ScrubPunchcard, and update this test.",
					rt.Name(), f.Name)
			}
		}
	}
}

// TestSessionsHasNoProjectLabels is the sibling guard for SessionsPayload:
// summary + per-date daily + duration histogram, no project / axis labels.
func TestSessionsHasNoProjectLabels(t *testing.T) {
	forbidden := map[string]struct{}{
		"Project": {}, "Projects": {},
		"Language": {}, "Languages": {},
		"Machine": {}, "Machines": {},
		"Editor": {}, "Editors": {},
		"Entity": {}, "Entities": {}, "Path": {}, "Paths": {}, "File": {}, "Files": {},
	}
	for _, rt := range []reflect.Type{
		reflect.TypeOf(model.SessionsPayload{}),
		reflect.TypeOf(model.SessionSummary{}),
		reflect.TypeOf(model.SessionDaily{}),
		reflect.TypeOf(model.SessionHistBin{}),
	} {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if _, bad := forbidden[f.Name]; bad {
				t.Errorf("%s gained forbidden public field %q — Sessions is documented as label-free in scrub.go. "+
					"If this field is intentional, update ScrubMomentum's docstring, add ScrubSessions, and update this test.",
					rt.Name(), f.Name)
			}
		}
	}
}

// TestScrub_NilAndEmpty exercises the fast paths.
func TestScrub_NilAndEmpty(t *testing.T) {
	if got := Scrub(nil, model.HiddenSetsMap{"project": {"x"}}); got != nil {
		t.Errorf("Scrub(nil, h) = %+v, want nil", got)
	}
	// nil hidden should return the input pointer unchanged.
	p := mkPayload()
	if got := Scrub(p, nil); got != p {
		t.Errorf("Scrub(p, nil) should return input pointer unchanged (no-op fast path)")
	}
	// No-op when the hide set has no relevant axes.
	empty := model.HiddenSetsMap{"project": nil, "language": {}}
	if got := Scrub(p, empty); got != p {
		t.Errorf("Scrub(p, empty) should return input pointer unchanged")
	}
}
