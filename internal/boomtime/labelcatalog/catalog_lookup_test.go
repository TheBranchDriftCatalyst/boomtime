// catalog_lookup_test.go — direct coverage for ByID/IDs (boom-se2.1).
//
// The pyramid these tests implement:
//   - Bidirectional consistency: every id in IDs() resolves via ByID
//   - Immutability: IDs() returns a fresh slice; mutating it doesn't
//     poison future calls
//   - Absence handling: ByID on unknown id returns (Entry{}, false),
//     never panics or errors
//
// Every assertion pins a specific invariant — no bare roundtrip tests.
// A missing ID string (`""`) or a made-up id must round to
// (Entry{}, false) so callers can treat the second return as a soft
// existence check without a nil-error dance.
package labelcatalog

import (
	// Named ginkgo import — dot-imported would shadow the package's
	// own Entry type with ginkgo's table-driven `Entry` helper.
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	Describe = ginkgo.Describe
	It       = ginkgo.It
)

var _ = Describe("catalog lookup (boom-se2.1)", func() {
	Describe("ByID", func() {
		It("returns (Entry{}, false) for an unknown id", func() {
			e, ok := ByID("does-not-exist-xyz-123")
			Expect(ok).To(BeFalse())
			Expect(e).To(Equal(Entry{}))
		})

		It("returns (Entry{}, false) for the empty string", func() {
			e, ok := ByID("")
			Expect(ok).To(BeFalse())
			Expect(e).To(Equal(Entry{}))
		})

		It("returns a real entry with a non-empty prompt for a known id", func() {
			// The specific ID is drawn from catalog.go — every catalog
			// entry MUST have a non-empty Prompt to be a valid image
			// generation seed. This test would fail if a catalog entry's
			// Prompt got accidentally emptied AND if ByID broke.
			for _, want := range Entries {
				e, ok := ByID(want.ID)
				Expect(ok).To(BeTrue(), "ByID(%q) unexpectedly missing", want.ID)
				Expect(e.ID).To(Equal(want.ID))
				Expect(e.Prompt).NotTo(BeEmpty(), "%s.Prompt is empty", want.ID)
			}
		})
	})

	Describe("IDs", func() {
		It("returns len(Entries) elements, no dupes", func() {
			ids := IDs()
			Expect(ids).To(HaveLen(len(Entries)))
			seen := map[string]int{}
			for _, id := range ids {
				seen[id]++
			}
			for id, n := range seen {
				Expect(n).To(Equal(1), "duplicate id %q appears %d times", id, n)
			}
		})

		It("preserves declaration order from Entries", func() {
			ids := IDs()
			for i, e := range Entries {
				Expect(ids[i]).To(Equal(e.ID),
					"index %d: IDs[%d]=%q vs Entries[%d].ID=%q — ordering drifted", i, i, ids[i], i, e.ID)
			}
		})

		It("returns a fresh slice — mutating it doesn't poison future calls", func() {
			// Load-bearing invariant. Callers (admin baseline endpoint at
			// handler/admin_label_images.go:87) rely on the returned
			// slice being safe to modify locally without corrupting the
			// package-level catalog.
			first := IDs()
			originalHead := first[0]
			first[0] = "MUTATED-BY-TEST"
			second := IDs()
			Expect(second[0]).To(Equal(originalHead),
				"IDs() returned an aliased slice — mutation to a previous return corrupted the next return")
		})
	})

	Describe("ByID ↔ IDs bidirectional consistency", func() {
		It("every id in IDs() resolves via ByID with matching Entry.ID", func() {
			for _, id := range IDs() {
				e, ok := ByID(id)
				Expect(ok).To(BeTrue(), "IDs() yielded %q but ByID rejected it", id)
				Expect(e.ID).To(Equal(id), "ByID(%q).ID != %q — search returned wrong entry", id, id)
			}
		})

		It("every Entry.ID appears in IDs() (no drop)", func() {
			ids := IDs()
			set := map[string]bool{}
			for _, id := range ids {
				set[id] = true
			}
			for _, e := range Entries {
				Expect(set[e.ID]).To(BeTrue(),
					"Entries has %q but IDs() dropped it", e.ID)
			}
		})
	})
})
