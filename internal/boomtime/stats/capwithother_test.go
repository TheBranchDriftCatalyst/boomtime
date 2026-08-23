// capwithother_ginkgo_test.go — ginkgo mirror of capwithother_test.go (boom-tst-ginkgo).
// 1:1 case map (7 stdlib TestXxx):
//
//	TestCapWithOtherSmallListUnchanged                → capWithOther > "small list (<= topN) is unchanged"
//	TestCapWithOtherCollapsesTail                     → capWithOther > "collapses tail into Other with element-wise sums"
//	TestCapWithOtherCarriesOtherMembers               → capWithOther > "Other carries members for tooltip breakdown (boom-7m4)"
//	TestCapWithOtherRespectsMembersCap                → capWithOther > "respects otherMembersCap (post boom-mwp-other)"
//	TestCapWithOtherGrowsToKeepOtherBelowCeiling      → capWithOther > "grows topN to keep Other <= 25% ceiling (boom-mwp-other)"
//	TestCapWithOtherHonorsDefaultNWhenOtherIsSmall    → capWithOther > "honors default N when Other is already small"
//	TestCapWithOtherSmallListHasNoOtherMembers        → capWithOther > "small list rows carry no Other* fields"
//	TestCapWithOtherDoesNotMutateInput                → capWithOther > "does not mutate caller's input slice or backing array"
package stats

import (
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("capWithOther", func() {
	It("returns small lists (<= topN) unchanged", func() {
		in := []model.ResourceStats{
			{Name: "a", TotalSeconds: 30},
			{Name: "b", TotalSeconds: 20},
			{Name: "c", TotalSeconds: 10},
		}
		out := capWithOther(in)
		Expect(out).To(HaveLen(3))
		for i, want := range []string{"a", "b", "c"} {
			Expect(out[i].Name).To(Equal(want), fmt.Sprintf("out[%d].Name (order preserved)", i))
		}
	})

	It("collapses tail into Other with element-wise TotalDaily/PctDaily sums", func() {
		// 14 resources, each with a 2-element daily array. TotalSeconds descending so
		// the sort keeps names r00..r11 as top-12 and r12,r13 fold into "Other".
		var in []model.ResourceStats
		for i := 0; i < 14; i++ {
			in = append(in, model.ResourceStats{
				Name:         string(rune('A' + i)),
				TotalSeconds: int64(1400 - i*100), // 1400, 1300, ... strictly descending
				TotalPct:     float64(i + 1),
				TotalDaily:   []int64{int64(i), int64(i * 2)},
				PctDaily:     []float64{float64(i), float64(i) * 0.5},
			})
		}

		out := capWithOther(in)

		Expect(out).To(HaveLen(13), "top-12 + Other")

		other := out[12]
		Expect(other.Name).To(Equal("Other (2 more)"))

		// Tail is the two lowest-TotalSeconds entries: i=12 and i=13.
		// TotalSeconds tail sum = 200 + 100 = 300.
		Expect(other.TotalSeconds).To(BeEquivalentTo(300))
		// TotalPct tail sum = 13 + 14 = 27.
		Expect(other.TotalPct).To(Equal(float64(27)))
		// TotalDaily element-wise: index0 = 12+13 = 25; index1 = 24+26 = 50.
		Expect(other.TotalDaily[0]).To(BeEquivalentTo(25))
		Expect(other.TotalDaily[1]).To(BeEquivalentTo(50))
		// PctDaily element-wise: index0 = 12+13 = 25; index1 = 6+6.5 = 12.5.
		Expect(other.PctDaily[0]).To(Equal(float64(25)))
		Expect(other.PctDaily[1]).To(Equal(float64(12.5)))
	})

	// boom-7m4: the synthesized "Other" entry must carry the tail members so
	// tooltips can render a breakdown.
	It("Other carries OtherMembers for tooltip breakdown (boom-7m4)", func() {
		var in []model.ResourceStats
		for i := 0; i < 14; i++ {
			in = append(in, model.ResourceStats{
				Name:         string(rune('A' + i)),
				TotalSeconds: int64(1400 - i*100),
				TotalPct:     float64(i + 1),
				TotalDaily:   []int64{int64(i)},
				PctDaily:     []float64{float64(i)},
			})
		}
		out := capWithOther(in)
		other := out[len(out)-1]

		Expect(other.OtherCount).To(BeEquivalentTo(2))
		Expect(other.OtherMembers).To(HaveLen(2))
		// Tail is desc by TotalSeconds: i=12 ('M', 200s, 13%), then i=13 ('N', 100s, 14%).
		Expect(other.OtherMembers[0].Name).To(Equal("M"))
		Expect(other.OtherMembers[0].TotalSeconds).To(BeEquivalentTo(200))
		Expect(other.OtherMembers[0].TotalPct).To(Equal(float64(13)))
		Expect(other.OtherMembers[1].Name).To(Equal("N"))
		Expect(other.OtherMembers[1].TotalSeconds).To(BeEquivalentTo(100))
		Expect(other.OtherMembers[1].TotalPct).To(Equal(float64(14)))
	})

	// The cap bounds the payload — a tail bigger than otherMembersCap only carries
	// the top otherMembersCap members, but OtherCount reflects the true tail size.
	It("respects otherMembersCap while OtherCount reports true tail size", func() {
		// Values 6500, 6400, ..., 100 → adaptive-N growth ends short of full list; tail > 20.
		var in []model.ResourceStats
		for i := 0; i < 65; i++ {
			in = append(in, model.ResourceStats{
				Name:         string(rune('a'+i%26)) + string(rune('0'+i/26)),
				TotalSeconds: int64(6500 - i*100), // strictly desc
				TotalDaily:   []int64{0},
				PctDaily:     []float64{0},
			})
		}
		out := capWithOther(in)
		other := out[len(out)-1]

		Expect(other.OtherMembers).To(HaveLen(20), "otherMembersCap")
		Expect(other.OtherCount).To(BeEquivalentTo(len(in) - (len(out) - 1)))
		// First member is the highest-TotalSeconds tail entry — index equal to
		// the adapted topN. Compute expected from the output length.
		expectedFirst := int64(6500 - (len(out)-1)*100)
		Expect(other.OtherMembers[0].TotalSeconds).To(Equal(expectedFirst))
	})

	// boom-mwp-other: Other shouldn't dominate — if the default top-12 would leave
	// Other above the ceiling, grow topN until it drops below OR we hit resourceMaxN.
	It("grows topN to keep Other share <= 25% ceiling (boom-mwp-other)", func() {
		// 40 entries, each 100s → grand total 4000. Default-N would give 70% Other.
		var in []model.ResourceStats
		for i := 0; i < 40; i++ {
			in = append(in, model.ResourceStats{
				Name:         fmt.Sprintf("r%02d", i),
				TotalSeconds: 100,
				TotalDaily:   []int64{0},
				PctDaily:     []float64{0},
			})
		}
		out := capWithOther(in)
		other := out[len(out)-1]

		// Sanity: Other's share must be ≤ ceiling (or we exhausted the list).
		var total int64
		for _, r := range in {
			total += r.TotalSeconds
		}
		share := float64(other.TotalSeconds) / float64(total)
		Expect(share).To(BeNumerically("<=", 0.25001), "Other share must be <= 0.25")
		// Sanity: at least resourceTopN entries kept (minimum floor).
		Expect(len(out)-1).To(BeNumerically(">=", 12), "kept at least resourceTopN entries in top")
	})

	It("honors default N when Other is already small", func() {
		// 15 entries — top-12 dominant, tail-3 tiny. Default-N Other << 30%.
		var in []model.ResourceStats
		for i := 0; i < 15; i++ {
			val := int64(10000)
			if i >= 12 {
				val = 10 // tiny tail
			}
			in = append(in, model.ResourceStats{
				Name:         "r" + string(rune('a'+i)),
				TotalSeconds: val,
				TotalDaily:   []int64{0},
				PctDaily:     []float64{0},
			})
		}
		out := capWithOther(in)
		// Should be 12 top + 1 Other = 13.
		Expect(out).To(HaveLen(13))
	})

	// Small (<= resourceTopN) lists take the fast path — no synthesized Other, and
	// no OtherMembers / OtherCount anywhere.
	It("small list rows carry no Other* fields", func() {
		in := []model.ResourceStats{
			{Name: "a", TotalSeconds: 30},
			{Name: "b", TotalSeconds: 20},
		}
		out := capWithOther(in)
		for _, r := range out {
			Expect(r.OtherCount).To(BeEquivalentTo(0))
			Expect(r.OtherMembers).To(BeNil())
		}
	})

	It("does not mutate the caller's input slice or backing array", func() {
		// 14 entries in ASCENDING TotalSeconds order (so the internal sort would
		// reorder them), backed by an array with one spare sentinel slot (so an
		// append into the caller's backing array would clobber it).
		backing := make([]model.ResourceStats, 15)
		for i := 0; i < 14; i++ {
			backing[i] = model.ResourceStats{
				Name:         string(rune('A' + i)),
				TotalSeconds: int64(100 * (i + 1)),
			}
		}
		backing[14] = model.ResourceStats{Name: "sentinel", TotalSeconds: -1}
		in := backing[:14]

		out := capWithOther(in)
		Expect(out).To(HaveLen(13), "top-12 + Other")

		for i := 0; i < 14; i++ {
			wantName := string(rune('A' + i))
			wantSecs := int64(100 * (i + 1))
			Expect(in[i].Name).To(Equal(wantName), fmt.Sprintf("input[%d] name mutated", i))
			Expect(in[i].TotalSeconds).To(Equal(wantSecs), fmt.Sprintf("input[%d] seconds mutated", i))
		}
		Expect(backing[14].Name).To(Equal("sentinel"))
		Expect(backing[14].TotalSeconds).To(BeEquivalentTo(-1))
	})
})
