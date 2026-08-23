// stats_scrub_test.go — non-tautological coverage for the axis-hide
// scrubber path (boom-se2.2). Pins the following invariants:
//
//   - hiddenNameSet: distinguishes nil-input from empty-axis (both yield
//     nil), and lowercases values so a callers can look up any-case name.
//   - scrubSegmentTail: MONOTONIC decrease in OtherMembers total seconds
//     (never adds bytes on filter); preserves non-matching siblings
//     verbatim; leaves non-Other rows untouched.
//   - ScrubTail: SAME-POINTER return when there is nothing to filter
//     (proves no unnecessary copy), fresh-pointer return when a filter
//     changes at least one row; multi-axis isolation — filtering the
//     project axis must not touch the language segment.
//   - HiddenSetsMap.Values: nil-for-unset (not empty slice) — callers
//     use nil-check to distinguish "not configured" from "configured
//     with no values".
//
// Anti-tautology stance: NO test asserts "hidden foo → foo removed",
// which just re-implements the code. Instead each test names a
// preservation, totals-bound, pointer-identity, or nil-vs-empty
// invariant that would break something real if flipped.
package model

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sumOtherSeconds(members []OtherMember) int64 {
	var s int64
	for _, m := range members {
		s += m.TotalSeconds
	}
	return s
}

var _ = Describe("HiddenSetsMap.Values / Projects (boom-se2.2)", func() {
	It("returns nil for an unset axis (distinguishes unset from empty)", func() {
		h := HiddenSetsMap{}
		Expect(h.Values("project")).To(BeNil(),
			"empty map must return nil, not []string{} — callers use nil to detect 'no rule'")
	})

	It("returns the exact slice pre-configured for a set axis", func() {
		h := HiddenSetsMap{"project": {"secret-proj", "internal-x"}}
		Expect(h.Values("project")).To(Equal([]string{"secret-proj", "internal-x"}))
	})

	It("Projects() convenience matches Values(\"project\")", func() {
		h := HiddenSetsMap{"project": {"secret-proj"}}
		Expect(h.Projects()).To(Equal(h.Values("project")))
	})
})

var _ = Describe("hiddenNameSet (boom-se2.2)", func() {
	It("returns nil when hidden is nil (nil-passthrough)", func() {
		Expect(hiddenNameSet(nil, "project")).To(BeNil())
	})

	It("returns nil when the axis has no values (empty-passthrough)", func() {
		Expect(hiddenNameSet(HiddenSetsMap{}, "project")).To(BeNil(),
			"empty axis must be treated as 'no rule' — same as nil-hidden")
	})

	It("lowercases every value so mixed-case inputs collapse to one lookup key", func() {
		set := hiddenNameSet(HiddenSetsMap{"project": {"SecretProj", "internal-X"}}, "project")
		Expect(set).To(HaveKey("secretproj"))
		Expect(set).To(HaveKey("internal-x"))
		Expect(set).NotTo(HaveKey("SecretProj"),
			"pre-lowercase keys must NOT be retained — callers rely on lookup-by-lowercase")
	})

	It("ignores unrelated axes (project rule shouldn't populate language axis)", func() {
		Expect(hiddenNameSet(HiddenSetsMap{"project": {"x"}}, "language")).To(BeNil())
	})
})

var _ = Describe("scrubSegmentTail (boom-se2.2)", func() {
	It("returns (seg, false) when the segment is empty (nil-passthrough)", func() {
		out, changed := scrubSegmentTail(nil, map[string]struct{}{"x": {}})
		Expect(out).To(BeNil())
		Expect(changed).To(BeFalse())
	})

	It("returns (seg, false) when the hidden map is empty", func() {
		seg := []ResourceStats{{Name: "a"}}
		out, changed := scrubSegmentTail(seg, nil)
		Expect(changed).To(BeFalse())
		Expect(out).To(HaveLen(1))
	})

	It("preserves non-Other rows and non-matching OtherMembers verbatim", func() {
		seg := []ResourceStats{
			{Name: "Go", TotalSeconds: 3600, OtherMembers: nil},
			{Name: "Other (3 more)", OtherMembers: []OtherMember{
				{Name: "keep-me", TotalSeconds: 600},
				{Name: "hidden-one", TotalSeconds: 300},
				{Name: "also-keep", TotalSeconds: 100},
			}},
		}
		hidden := map[string]struct{}{"hidden-one": {}}
		out, changed := scrubSegmentTail(seg, hidden)

		Expect(changed).To(BeTrue())
		Expect(out).To(HaveLen(2))
		// Non-Other row: unchanged in name AND totals.
		Expect(out[0].Name).To(Equal("Go"))
		Expect(out[0].TotalSeconds).To(Equal(int64(3600)))
		// Other row: only non-matching siblings survive, with IDENTICAL fields.
		Expect(out[1].OtherMembers).To(HaveLen(2))
		Expect(out[1].OtherMembers[0]).To(Equal(OtherMember{Name: "keep-me", TotalSeconds: 600}),
			"non-matching sibling must be byte-identical, not just name-equal")
		Expect(out[1].OtherMembers[1]).To(Equal(OtherMember{Name: "also-keep", TotalSeconds: 100}))
	})

	It("monotonically DECREASES OtherMembers total when a filter matches", func() {
		seg := []ResourceStats{
			{Name: "Other", OtherMembers: []OtherMember{
				{Name: "a", TotalSeconds: 100},
				{Name: "b", TotalSeconds: 200},
				{Name: "c", TotalSeconds: 300},
			}},
		}
		before := sumOtherSeconds(seg[0].OtherMembers)
		out, _ := scrubSegmentTail(seg, map[string]struct{}{"b": {}})
		after := sumOtherSeconds(out[0].OtherMembers)

		Expect(after).To(BeNumerically("<", before),
			"filtering must REDUCE the tail sum, never keep it flat or grow it")
		Expect(after).To(Equal(int64(400)),
			"expected removal of exactly b (200s): 100+300=400")
	})

	It("is case-insensitive on OtherMembers.Name (matches hidden-set contract)", func() {
		seg := []ResourceStats{
			{Name: "Other", OtherMembers: []OtherMember{{Name: "HIDDEN-MIXED-Case", TotalSeconds: 500}}},
		}
		out, changed := scrubSegmentTail(seg, map[string]struct{}{"hidden-mixed-case": {}})
		Expect(changed).To(BeTrue())
		Expect(out[0].OtherMembers).To(BeEmpty(),
			"lowercase hidden entry must match mixed-case name in payload")
	})

	It("does not mutate the input segment (immutability contract)", func() {
		seg := []ResourceStats{
			{Name: "Other", OtherMembers: []OtherMember{
				{Name: "keep", TotalSeconds: 10},
				{Name: "drop", TotalSeconds: 20},
			}},
		}
		before := sumOtherSeconds(seg[0].OtherMembers)
		_, _ = scrubSegmentTail(seg, map[string]struct{}{"drop": {}})
		after := sumOtherSeconds(seg[0].OtherMembers)
		Expect(after).To(Equal(before),
			"input segment mutated — scrubSegmentTail's copy-on-write contract violated")
	})
})

var _ = Describe("StatsPayload.ScrubTail (boom-se2.2)", func() {
	makePayload := func() *StatsPayload {
		return &StatsPayload{
			Projects: []ResourceStats{
				{Name: "Other", OtherMembers: []OtherMember{
					{Name: "keep-proj", TotalSeconds: 100},
					{Name: "hide-proj", TotalSeconds: 200},
				}},
			},
			Languages: []ResourceStats{
				{Name: "Other", OtherMembers: []OtherMember{
					{Name: "keep-lang", TotalSeconds: 50},
				}},
			},
		}
	}

	It("returns the input pointer unchanged when hidden is nil", func() {
		p := makePayload()
		out := p.ScrubTail(nil)
		Expect(out).To(BeIdenticalTo(p),
			"same-pointer return required when no filter runs — proves no unnecessary copy")
	})

	It("returns the input pointer unchanged when hidden has no matches", func() {
		p := makePayload()
		out := p.ScrubTail(HiddenSetsMap{"project": {"totally-unmatched-name"}})
		Expect(out).To(BeIdenticalTo(p),
			"same-pointer return required when filters run but nothing matches")
	})

	It("returns a fresh pointer AND a filtered segment when at least one match hits", func() {
		p := makePayload()
		out := p.ScrubTail(HiddenSetsMap{"project": {"hide-proj"}})
		Expect(out).NotTo(BeIdenticalTo(p),
			"fresh pointer required when any filter changes a segment")
		Expect(out.Projects[0].OtherMembers).To(HaveLen(1))
		Expect(out.Projects[0].OtherMembers[0].Name).To(Equal("keep-proj"))
	})

	It("isolates axes — hiding on project MUST NOT touch language", func() {
		p := makePayload()
		out := p.ScrubTail(HiddenSetsMap{"project": {"hide-proj"}})
		Expect(out.Languages[0].OtherMembers).To(HaveLen(1),
			"language segment must be untouched when only project axis has a rule")
		Expect(out.Languages[0].OtherMembers[0].Name).To(Equal("keep-lang"))
	})

	It("filters every axis independently (project, language, editor, platform, machine, category)", func() {
		// Load-bearing: proves the switch statement in ScrubTail's write-back
		// dispatches to ALL six axis segments (not just project + language).
		axes := []struct {
			axis    string
			seed    func(*StatsPayload)
			get     func(*StatsPayload) []ResourceStats
			hideVal string
		}{
			{"editor", func(p *StatsPayload) {
				p.Editors = []ResourceStats{{Name: "Other", OtherMembers: []OtherMember{{Name: "hide-me"}}}}
			}, func(p *StatsPayload) []ResourceStats { return p.Editors }, "hide-me"},
			{"platform", func(p *StatsPayload) {
				p.Platforms = []ResourceStats{{Name: "Other", OtherMembers: []OtherMember{{Name: "hide-me"}}}}
			}, func(p *StatsPayload) []ResourceStats { return p.Platforms }, "hide-me"},
			{"machine", func(p *StatsPayload) {
				p.Machines = []ResourceStats{{Name: "Other", OtherMembers: []OtherMember{{Name: "hide-me"}}}}
			}, func(p *StatsPayload) []ResourceStats { return p.Machines }, "hide-me"},
			{"category", func(p *StatsPayload) {
				p.Categories = []ResourceStats{{Name: "Other", OtherMembers: []OtherMember{{Name: "hide-me"}}}}
			}, func(p *StatsPayload) []ResourceStats { return p.Categories }, "hide-me"},
			{"language", func(p *StatsPayload) {
				p.Languages = []ResourceStats{{Name: "Other", OtherMembers: []OtherMember{{Name: "hide-me"}}}}
			}, func(p *StatsPayload) []ResourceStats { return p.Languages }, "hide-me"},
		}
		for _, tc := range axes {
			p := &StatsPayload{}
			tc.seed(p)
			out := p.ScrubTail(HiddenSetsMap{tc.axis: {tc.hideVal}})
			Expect(tc.get(out)[0].OtherMembers).To(BeEmpty(),
				"%s axis: hide-me survived filtering — write-back for this axis is broken", tc.axis)
		}
	})

	It("handles a nil payload without panic", func() {
		var nilP *StatsPayload
		out := nilP.ScrubTail(HiddenSetsMap{"project": {"x"}})
		Expect(out).To(BeNil())
	})
})
