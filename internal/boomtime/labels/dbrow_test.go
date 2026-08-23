// dbrow_ginkgo_test.go — ginkgo mirror of dbrow_test.go (boom-tst-ginkgo).
//
// PARALLEL migration: both this file and dbrow_test.go run under
// `go test ./internal/labels/...`. Once every stdlib TestXxx has a
// verified 1:1 ginkgo equivalent in the same package, the stdlib file
// gets deleted in a single "kill switch" commit at the end of the epic.
//
// Assertion mapping (see docs/testing/ginkgo.md for the full guide):
//   if got != want { t.Errorf(...) }        →  Expect(got).To(Equal(want))
//   if err != nil  { t.Fatalf(...) }        →  Expect(err).NotTo(HaveOccurred())
//   if !ok { t.Error(...) }                 →  Expect(cond).To(BeTrue())
//   if _, ok := x.(T); !ok { t.Errorf... }  →  Expect(x).To(BeAssignableToTypeOf(T{}))
//   Table-driven stdlib for-loop            →  DescribeTable + Entry per row
//
// Naming convention: <original>_ginkgo_test.go per file during the
// parallel window. Renamed back to <original>_test.go after stdlib
// deletion.

package labels

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SpecFromDBRow", func() {

	Describe("deriveTierKey", func() {
		// Was: TestDeriveTierKey_Convention (table-driven for-loop).
		DescribeTable("id + tier → key",
			func(id, tier, want string) {
				Expect(deriveTierKey(id, tier)).To(Equal(want))
			},
			Entry("standard tier id", "languages-python-master", "master", "languages:python"),
			Entry("editor + legend", "editors-vim-legend", "legend", "editors:vim"),
			Entry("value with special chars", "languages-c++-adept", "adept", "languages:c++"),
			Entry("no trailing tier match → empty", "languages-python-master", "novice", ""),
			Entry("no dash to split axis from value → empty", "solo-master", "master", ""),
			Entry("empty tier → empty key", "whatever", "", ""),
		)
	})

	// Was: TestSpecFromDBRow_TierRowGetsTierKey.
	Context("on a tier row", func() {
		It("populates TierKey from the id and decodes the condition", func() {
			row := DBRow{
				ID:        "languages-python-master",
				Kind:      "tier",
				Label:     "PYTHON MASTER",
				Rank:      100,
				Tier:      "master",
				Condition: json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":100}`),
			}
			spec, err := SpecFromDBRow(row)
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.TierKey).To(Equal("languages:python"))
			Expect(spec.Condition).To(BeAssignableToTypeOf(AxisTimeCond{}))
		})
	})

	// Was: TestSpecFromDBRow_NonTierLeavesTierKeyEmpty.
	Context("on a non-tier row", func() {
		It("leaves TierKey empty", func() {
			row := DBRow{
				ID:        "night-watch",
				Kind:      "archetype",
				Label:     "NIGHT WATCH",
				Rank:      50,
				Condition: json.RawMessage(`{"kind":"punchcard-hour-pct","hoursIn":[22,23,0,1,2,3,4,5],"op":">=","pct":0.4}`),
			}
			spec, err := SpecFromDBRow(row)
			Expect(err).NotTo(HaveOccurred())
			Expect(spec.TierKey).To(BeEmpty())
		})
	})
})

var _ = Describe("SpecsFromDBRows", func() {
	// Was: TestSpecsFromDBRows_BadConditionRejects.
	Context("with one bad row (unknown kind)", func() {
		It("rejects the whole batch loudly", func() {
			rows := []DBRow{
				{ID: "ok", Kind: "archetype",
					Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`)},
				{ID: "bad", Kind: "archetype",
					Condition: json.RawMessage(`{"kind":"UNKNOWN"}`)},
			}
			_, err := SpecsFromDBRows(rows)
			Expect(err).To(HaveOccurred())
		})
	})
})
