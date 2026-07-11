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
func TestCapWithOtherRespectsMembersCap(t *testing.T) {
	// resourceTopN = 12; otherMembersCap = 20. Use 12+25 = 37 entries so the
	// tail (25) exceeds the cap.
	var in []model.ResourceStats
	for i := 0; i < 37; i++ {
		in = append(in, model.ResourceStats{
			Name:         string(rune('a' + i%26)) + string(rune('0'+i/26)),
			TotalSeconds: int64(3700 - i*100), // strictly desc
			TotalDaily:   []int64{0},
			PctDaily:     []float64{0},
		})
	}
	out := capWithOther(in)
	other := out[len(out)-1]

	if other.OtherCount != 25 {
		t.Errorf("Other.OtherCount = %d, want 25 (len(tail))", other.OtherCount)
	}
	if len(other.OtherMembers) != 20 {
		t.Fatalf("len(Other.OtherMembers) = %d, want %d (otherMembersCap)", len(other.OtherMembers), 20)
	}
	// The first member is the highest-TotalSeconds tail entry (index 12 in the
	// desc-sorted list).
	if other.OtherMembers[0].TotalSeconds != 2500 {
		t.Errorf("OtherMembers[0].TotalSeconds = %d, want 2500 (first tail entry)", other.OtherMembers[0].TotalSeconds)
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
