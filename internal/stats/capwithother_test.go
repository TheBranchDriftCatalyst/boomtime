package stats

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

func TestCapWithOtherSmallListUnchanged(t *testing.T) {
	in := []model.ResourceStats{
		{Name: "a", TotalSeconds: 30},
		{Name: "b", TotalSeconds: 20},
		{Name: "c", TotalSeconds: 10},
	}
	out := capWithOther(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3 (<=12 unchanged)", len(out))
	}
	for i, want := range []string{"a", "b", "c"} {
		if out[i].Name != want {
			t.Errorf("out[%d].Name = %q, want %q (order preserved)", i, out[i].Name, want)
		}
	}
}

func TestCapWithOtherCollapsesTail(t *testing.T) {
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

	if len(out) != 13 {
		t.Fatalf("len = %d, want 13 (top-12 + Other)", len(out))
	}

	other := out[12]
	if other.Name != "Other (2 more)" {
		t.Fatalf("trailing name = %q, want %q", other.Name, "Other (2 more)")
	}

	// Tail is the two lowest-TotalSeconds entries: i=12 and i=13.
	// TotalSeconds tail sum = 200 + 100 = 300.
	if other.TotalSeconds != 300 {
		t.Errorf("Other.TotalSeconds = %d, want 300", other.TotalSeconds)
	}
	// TotalPct tail sum = 13 + 14 = 27.
	if other.TotalPct != 27 {
		t.Errorf("Other.TotalPct = %v, want 27", other.TotalPct)
	}
	// TotalDaily element-wise: index0 = 12+13 = 25; index1 = 24+26 = 50.
	if other.TotalDaily[0] != 25 || other.TotalDaily[1] != 50 {
		t.Errorf("Other.TotalDaily = %v, want [25 50]", other.TotalDaily)
	}
	// PctDaily element-wise: index0 = 12+13 = 25; index1 = 6+6.5 = 12.5.
	if other.PctDaily[0] != 25 || other.PctDaily[1] != 12.5 {
		t.Errorf("Other.PctDaily = %v, want [25 12.5]", other.PctDaily)
	}
}

// gaka-7m4: the synthesized "Other" entry must carry the tail members so
// tooltips can render a breakdown.
func TestCapWithOtherCarriesOtherMembers(t *testing.T) {
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

	if other.OtherCount != 2 {
		t.Errorf("Other.OtherCount = %d, want 2 (len(tail))", other.OtherCount)
	}
	if len(other.OtherMembers) != 2 {
		t.Fatalf("len(Other.OtherMembers) = %d, want 2", len(other.OtherMembers))
	}
	// Tail is desc by TotalSeconds: i=12 ('M', 200s, 13%), then i=13 ('N', 100s, 14%).
	if other.OtherMembers[0].Name != "M" || other.OtherMembers[0].TotalSeconds != 200 || other.OtherMembers[0].TotalPct != 13 {
		t.Errorf("OtherMembers[0] = %+v, want {M 200 13}", other.OtherMembers[0])
	}
	if other.OtherMembers[1].Name != "N" || other.OtherMembers[1].TotalSeconds != 100 || other.OtherMembers[1].TotalPct != 14 {
		t.Errorf("OtherMembers[1] = %+v, want {N 100 14}", other.OtherMembers[1])
	}
}

// The cap bounds the payload — a tail bigger than otherMembersCap only carries
// the top otherMembersCap members, but OtherCount reflects the true tail size.
//
// Post gaka-mwp-other: the adaptive Other-share cap (otherMaxShare = 30%)
// grows topN beyond the default 12 when the default-N Other would dominate.
// Fixture: 60 entries with strictly-desc values so the tail is long enough to
// exceed the members cap AFTER the adaptive growth stops.
func TestCapWithOtherRespectsMembersCap(t *testing.T) {
	// Values 6000, 5900, ..., 100 → grand total 183,000. Default-N Other share
	// would be far above 30%, driving topN up to resourceMaxN (40). Tail is
	// then 60-40 = 20 entries — exactly the members cap; the test's original
	// intent (tail > cap) needs even more entries. Use 65 to be safe.
	var in []model.ResourceStats
	for i := 0; i < 65; i++ {
		in = append(in, model.ResourceStats{
			Name:         string(rune('a' + i%26)) + string(rune('0'+i/26)),
			TotalSeconds: int64(6500 - i*100), // strictly desc
			TotalDaily:   []int64{0},
			PctDaily:     []float64{0},
		})
	}
	out := capWithOther(in)
	other := out[len(out)-1]

	if len(other.OtherMembers) != 20 {
		t.Fatalf("len(Other.OtherMembers) = %d, want %d (otherMembersCap)", len(other.OtherMembers), 20)
	}
	if other.OtherCount != len(in)-(len(out)-1) {
		t.Errorf("Other.OtherCount = %d, want %d (len(tail))", other.OtherCount, len(in)-(len(out)-1))
	}
	// First member is the highest-TotalSeconds tail entry — index equal to
	// the adapted topN. Compute expected from the output length.
	expectedFirst := int64(6500 - (len(out)-1)*100)
	if other.OtherMembers[0].TotalSeconds != expectedFirst {
		t.Errorf("OtherMembers[0].TotalSeconds = %d, want %d (first tail entry)", other.OtherMembers[0].TotalSeconds, expectedFirst)
	}
}

// gaka-mwp-other: Other shouldn't dominate — if the default top-12 would leave
// Other above 30%, grow topN until it drops below OR we hit resourceMaxN.
func TestCapWithOtherGrowsToKeepOtherBelow30Pct(t *testing.T) {
	// 30 entries, each 100s → grand total 3000. If we took the default top-12,
	// Other = 18*100 = 1800, share = 60% (way above 30%). Adaptive N must grow.
	var in []model.ResourceStats
	for i := 0; i < 30; i++ {
		in = append(in, model.ResourceStats{
			Name:         "r" + string(rune('a'+i)),
			TotalSeconds: 100,
			TotalDaily:   []int64{0},
			PctDaily:     []float64{0},
		})
	}
	out := capWithOther(in)
	other := out[len(out)-1]

	// Sanity: Other's share must be ≤ 30% (or we exhausted the list).
	var total int64
	for _, r := range in {
		total += r.TotalSeconds
	}
	share := float64(other.TotalSeconds) / float64(total)
	if share > 0.30001 {
		t.Errorf("Other share = %.3f, want ≤ 0.30", share)
	}
	// Sanity: at least resourceTopN entries kept (minimum floor).
	if len(out)-1 < 12 {
		t.Errorf("kept %d entries in top, want ≥ 12 (resourceTopN)", len(out)-1)
	}
}

// If the tail is small enough that default-N already gives Other ≤ 30%, keep
// topN at 12 — no unnecessary growth.
func TestCapWithOtherHonorsDefaultNWhenOtherIsSmall(t *testing.T) {
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
	if len(out) != 13 {
		t.Errorf("len(out) = %d, want 13 (default topN + Other)", len(out))
	}
}

// Small (<= resourceTopN) lists take the fast path — no synthesized Other, and
// no OtherMembers / OtherCount anywhere.
func TestCapWithOtherSmallListHasNoOtherMembers(t *testing.T) {
	in := []model.ResourceStats{
		{Name: "a", TotalSeconds: 30},
		{Name: "b", TotalSeconds: 20},
	}
	out := capWithOther(in)
	for i, r := range out {
		if r.OtherCount != 0 || r.OtherMembers != nil {
			t.Errorf("out[%d] = %+v, want no Other* fields on small-list rows", i, r)
		}
	}
}

func TestCapWithOtherDoesNotMutateInput(t *testing.T) {
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
	if len(out) != 13 {
		t.Fatalf("len(out) = %d, want 13 (top-12 + Other)", len(out))
	}

	for i := 0; i < 14; i++ {
		wantName := string(rune('A' + i))
		wantSecs := int64(100 * (i + 1))
		if in[i].Name != wantName || in[i].TotalSeconds != wantSecs {
			t.Errorf("input[%d] mutated: got {%q %d}, want {%q %d}",
				i, in[i].Name, in[i].TotalSeconds, wantName, wantSecs)
		}
	}
	if backing[14].Name != "sentinel" || backing[14].TotalSeconds != -1 {
		t.Errorf("caller's backing array written past len: %+v", backing[14])
	}
}
