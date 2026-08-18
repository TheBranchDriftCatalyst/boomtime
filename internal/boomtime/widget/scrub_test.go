// scrub_ginkgo_test.go — ginkgo mirror of scrub_test.go (gaka-0vp).
// 1:1 case map (10 stdlib TestXxx → 10 Its across 4 Describe groups):
//
//	TestScrub_HiddenProjectNeverAppears           → "Scrub" > It "strips a hidden project everywhere and does not mutate input"
//	TestScrub_HiddenLanguageNeverAppears          → "Scrub" > It "strips a hidden language without cross-axis contamination"
//	TestScrub_HiddenMachineNeverAppears           → "Scrub" > It "strips a hidden machine from the tail"
//	TestScrub_StatsPayloadNeverExposesFilePaths   → "public-safe payload contracts" > It "StatsPayload has no per-file/per-branch/per-entity field"
//	TestScrub_Idempotent                          → "Scrub" > It "is idempotent"
//	TestScrub_NilAndEmpty                         → "Scrub" > It "no-op fast paths (nil payload / nil hidden / empty axes)"
//	TestScrubMomentum_HiddenProjectNeverAppears   → "ScrubMomentum" > It "drops hidden project rows, keeps Weeks axis, preserves visible projects"
//	TestScrubMomentum_NoOpFastPaths               → "ScrubMomentum" > It "no-op fast paths"
//	TestScrubMomentum_Idempotent                  → "ScrubMomentum" > It "is idempotent"
//	TestPunchcardHasNoProjectLabels               → "public-safe payload contracts" > It "PunchcardPayload/Cell have no axis-label fields"
//	TestSessionsHasNoProjectLabels                → "public-safe payload contracts" > It "SessionsPayload family has no axis-label fields"
package widget

import (
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

var _ = Describe("Scrub", func() {
	// TestScrub_HiddenProjectNeverAppears: a hidden project name must not
	// appear anywhere in the scrubbed payload. The DB predicate already dropped
	// a hidden project from top-N (we simulate that by only placing "hakatime"
	// in the tail) but the OtherMembers tooltip payload would still leak it.
	It("strips a hidden project everywhere and does not mutate input", func() {
		p := mkPayload()
		Expect(containsName(p, "hakatime")).To(BeTrue(), "fixture bug: hakatime should be in the input payload's project tail")

		hidden := model.HiddenSetsMap{"project": {"hakatime"}}
		got := Scrub(p, hidden)
		Expect(containsName(got, "hakatime")).To(BeFalse(), "hidden project 'hakatime' leaked in scrubbed payload: %+v", got.Projects)
		// Non-hidden tail members must survive.
		Expect(containsName(got, "shown-b")).To(BeTrue(), "scrub over-filtered: dropped non-hidden tail member 'shown-b'")
		Expect(containsName(got, "shown-c")).To(BeTrue(), "scrub over-filtered: dropped non-hidden tail member 'shown-c'")
		// The input payload MUST NOT be mutated.
		Expect(containsName(p, "hakatime")).To(BeTrue(), "Scrub mutated the input payload — must return a copy for filtered rows")
	})

	// TestScrub_HiddenLanguageNeverAppears: same as above for the language axis.
	// Also asserts that a hide rule on one axis does NOT touch other axes' tails.
	It("strips a hidden language without cross-axis contamination", func() {
		p := mkPayload()
		hidden := model.HiddenSetsMap{"language": {"haskell"}}
		got := Scrub(p, hidden)
		Expect(containsName(got, "Haskell")).To(BeFalse(), "hidden language 'Haskell' leaked: %+v", got.Languages)
		// Sibling axis tails must remain untouched — a language hide MUST NOT
		// drop a project tail entry that happens to share a name.
		Expect(containsName(got, "hakatime")).To(BeTrue(), "Scrub cross-contaminated axes: language-hide dropped project tail entry")
		// A non-hidden language ("Rust") in the same tail must survive.
		Expect(containsName(got, "Rust")).To(BeTrue(), "Scrub over-filtered languages: dropped non-hidden 'Rust'")
	})

	// TestScrub_HiddenMachineNeverAppears: guards the "no raw machine
	// identifiers" clause (contract 3). A curated machine must not appear in
	// the tail.
	It("strips a hidden machine from the tail", func() {
		p := mkPayload()
		hidden := model.HiddenSetsMap{"machine": {"secret-box"}}
		got := Scrub(p, hidden)
		Expect(containsName(got, "SECRET-BOX")).To(BeFalse(), "hidden machine leaked in tail: %+v", got.Machines)
	})

	// TestScrub_Idempotent: Scrub(Scrub(p, h), h) == Scrub(p, h).
	It("is idempotent", func() {
		p := mkPayload()
		hidden := model.HiddenSetsMap{
			"project":  {"hakatime"},
			"language": {"haskell"},
		}
		once := Scrub(p, hidden)
		twice := Scrub(once, hidden)
		Expect(reflect.DeepEqual(once, twice)).To(BeTrue(), "Scrub not idempotent:\nonce=%+v\ntwice=%+v", once, twice)
	})

	// TestScrub_NilAndEmpty exercises the fast paths.
	It("no-op fast paths (nil payload / nil hidden / empty axes)", func() {
		Expect(Scrub(nil, model.HiddenSetsMap{"project": {"x"}})).To(BeNil(), "Scrub(nil, h) should be nil")
		p := mkPayload()
		// nil hidden should return the input pointer unchanged.
		Expect(Scrub(p, nil)).To(BeIdenticalTo(p), "Scrub(p, nil) should return input pointer unchanged (no-op fast path)")
		// No-op when the hide set has no relevant axes.
		empty := model.HiddenSetsMap{"project": nil, "language": {}}
		Expect(Scrub(p, empty)).To(BeIdenticalTo(p), "Scrub(p, empty) should return input pointer unchanged")
	})
})

var _ = Describe("ScrubMomentum", func() {
	// TestScrubMomentum_HiddenProjectNeverAppears: MomentumPayload carries per-
	// project rows keyed by project name; a hidden project must not appear in
	// the scrubbed momentum payload (bd gaka-6jm.6).
	It("drops hidden project rows, keeps Weeks axis, preserves visible projects", func() {
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
			Expect(strings.EqualFold(p.Name, "hakatime")).To(BeFalse(),
				"hidden project 'hakatime' leaked in momentum payload: %+v", got.Projects)
		}
		// Non-hidden projects must survive.
		names := map[string]bool{}
		for _, p := range got.Projects {
			names[p.Name] = true
		}
		Expect(names["public-a"]).To(BeTrue(), "ScrubMomentum over-filtered: dropped visible 'public-a'; got %+v", got.Projects)
		Expect(names["public-b"]).To(BeTrue(), "ScrubMomentum over-filtered: dropped visible 'public-b'; got %+v", got.Projects)
		// Input MUST NOT be mutated.
		found := false
		for _, p := range mp.Projects {
			if p.Name == "hakatime" {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "ScrubMomentum mutated its input — must return a copy for filtered rows")
		// Weeks axis is temporal and must be preserved verbatim.
		Expect(got.Weeks).To(HaveLen(len(mp.Weeks)), "ScrubMomentum altered Weeks axis: got %v, want %v", got.Weeks, mp.Weeks)
	})

	// TestScrubMomentum_NoOpFastPaths exercises the return-input-unchanged cases.
	It("no-op fast paths (nil payload / nil hidden / empty axes / no matches)", func() {
		Expect(ScrubMomentum(nil, model.HiddenSetsMap{"project": {"x"}})).To(BeNil(),
			"ScrubMomentum(nil, h) should be nil")
		mp := &model.MomentumPayload{
			Projects: []model.MomentumProject{{Name: "public-a", TotalSeconds: 100}},
		}
		Expect(ScrubMomentum(mp, nil)).To(BeIdenticalTo(mp),
			"ScrubMomentum(mp, nil) should return input pointer unchanged")
		empty := model.HiddenSetsMap{"project": nil}
		Expect(ScrubMomentum(mp, empty)).To(BeIdenticalTo(mp),
			"ScrubMomentum(mp, empty) should return input pointer unchanged")
		// No matching hidden values → return input pointer unchanged (fast path).
		hidden := model.HiddenSetsMap{"project": {"not-in-payload"}}
		Expect(ScrubMomentum(mp, hidden)).To(BeIdenticalTo(mp),
			"ScrubMomentum with no matches should return input pointer unchanged")
	})

	// TestScrubMomentum_Idempotent: ScrubMomentum(ScrubMomentum(m, h), h) ==
	// ScrubMomentum(m, h).
	It("is idempotent", func() {
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
		Expect(reflect.DeepEqual(once, twice)).To(BeTrue(),
			"ScrubMomentum not idempotent:\nonce=%+v\ntwice=%+v", once, twice)
	})
})

// The public-safe field guards are compile-time / reflect-time regression
// tests that assert forbidden field names never leak onto the wire payloads
// exported by the widget package.
var _ = Describe("public-safe payload contracts", func() {
	// TestScrub_StatsPayloadNeverExposesFilePaths — contract clauses 1-3.
	It("StatsPayload has no per-file/per-branch/per-entity field", func() {
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
			_, bad := forbidden[f.Name]
			Expect(bad).To(BeFalse(),
				"StatsPayload gained forbidden public field %q — public widget contract violated. "+
					"If this field is intentional, update internal/widget/scrub.go's contract AND its Scrub "+
					"implementation to strip/curate it, then update this test.", f.Name)
		}
	})

	// TestPunchcardHasNoProjectLabels — Punchcard MUST remain pure temporal
	// (dow×hour) aggregate.
	It("PunchcardPayload/Cell have no axis-label fields", func() {
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
				_, bad := forbidden[f.Name]
				Expect(bad).To(BeFalse(),
					"%s gained forbidden public field %q — Punchcard is documented as pure temporal in scrub.go. "+
						"If this field is intentional, update ScrubMomentum's docstring, add ScrubPunchcard, and update this test.",
					rt.Name(), f.Name)
			}
		}
	})

	// TestSessionsHasNoProjectLabels — Sessions is summary + per-date daily +
	// duration histogram, no project / axis labels.
	It("SessionsPayload family has no axis-label fields", func() {
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
				_, bad := forbidden[f.Name]
				Expect(bad).To(BeFalse(),
					"%s gained forbidden public field %q — Sessions is documented as label-free in scrub.go. "+
						"If this field is intentional, update ScrubMomentum's docstring, add ScrubSessions, and update this test.",
					rt.Name(), f.Name)
			}
		}
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
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
