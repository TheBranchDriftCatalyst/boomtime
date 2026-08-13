// eval_reading_test.go — validator specs for the reading-source time leaf
// (additive; the coding path is covered by eval_test.go). package goals
// (internal) so it can drive ValidateSpec directly, mirroring the existing
// unit-level ginkgo file.
package goals

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateSpec (reading-source time leaf)", func() {
	It("accepts a reading-source weekly time goal (no axis, hours-as-seconds target)", func() {
		// 5 hours/week = 18000 seconds.
		spec := `{"kind":"time","source":"reading","op":">=","target_seconds":18000,"window":"week"}`
		p, err := ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Source).To(Equal("reading"))
		Expect(p.Axis).To(Equal(""))
	})

	It("accepts an explicit source=coding leaf identically to the implicit default", func() {
		spec := `{"kind":"time","source":"coding","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`
		_, err := ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("rejects invalid reading/source shapes before persistence",
		func(spec, want string) {
			_, err := ValidateSpec(json.RawMessage(spec))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		// The headline validation guarantee: an unknown source is rejected.
		Entry("unknown source",
			`{"kind":"time","source":"chicken","op":">=","target_seconds":1,"window":"week"}`,
			"unknown source"),
		// v1 total-listening: a reading leaf must NOT carry an axis filter.
		Entry("reading source with an axis filter",
			`{"kind":"time","source":"reading","axis":"language","op":">=","target_seconds":1,"window":"week"}`,
			"does not support an axis filter"),
		// The generic invariants still apply on the reading arm.
		Entry("reading source, negative target",
			`{"kind":"time","source":"reading","op":">=","target_seconds":-1,"window":"week"}`,
			"non-negative"),
		Entry("reading source, unknown window",
			`{"kind":"time","source":"reading","op":">=","target_seconds":1,"window":"decade"}`,
			"unknown window"),
		Entry("reading source, unknown op",
			`{"kind":"time","source":"reading","op":"!=","target_seconds":1,"window":"week"}`,
			"unknown op"),
	)

	// A reading leaf nested inside a group must recurse-validate the same way a
	// coding leaf does — an unknown source in the 2nd child rejects the tree.
	It("recurses into group children (unknown source in a reading leaf rejects)", func() {
		bad := `{"kind":"all","of":[
			{"kind":"time","source":"reading","op":">=","target_seconds":1,"window":"week"},
			{"kind":"time","source":"bogus","op":">=","target_seconds":1,"window":"week"}
		]}`
		_, err := ValidateSpec(json.RawMessage(bad))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown source"))
	})
})
