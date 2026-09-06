// commits_attribution_test.go — boom-gsnv regression.
//
// Named invariant: "an inter-commit window with zero heartbeats must not shift
// its neighbours' coding time onto the wrong commits."
//
// This is DB-backed on purpose: the defect lived in the SQL, not in Go. The old
// get_time_between.sql inner-joined heartbeats and grouped by (min_date,
// max_date) with no ORDER BY, so
//
//	(a) a window matching zero heartbeats emitted NO ROW — the result slice was
//	    shorter than the window list, and
//	(b) group order was plan-dependent, while GetTotalTimeBetween blindly
//	    REVERSED whatever arrived.
//
// commits.go then zipped the slice positionally onto its commit gaps. The
// shape below (populated / EMPTY / populated, with the two populated windows
// carrying DISTINCT totals) is the minimum that catches both legs at once: a
// dropped row shifts the tail, and a reversal swaps 120 with 300.
package stats_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// commitAt builds the minimal CommitPayload the report pipeline reads: a sha
// and an author date.
func commitAt(sha string, at time.Time) model.CommitPayload {
	var p model.CommitPayload
	p.Sha = sha
	p.Commit.Author.Date = at
	return p
}

var _ = Describe("commit report time attribution (boom-gsnv)", func() {
	It("attributes each window's coding time to its own commit when a middle window has NO heartbeats", func() {
		hz := testutil.NewHarness(GinkgoT())
		owner, _ := hz.MintUser("commit_attr_gap")
		ctx := context.Background()
		const project = "boomtime"

		// Four commits, newest first — exactly how api.github.com returns them.
		//   window0 = [t1,t0] closes C0   (populated: 120s)
		//   window1 = [t2,t1] closes C1   (EMPTY — e.g. a CI/docs-only commit)
		//   window2 = [t3,t2] closes C2   (populated: 300s)
		// C3 only bounds window2; it never receives a total.
		base := time.Date(2025, 3, 10, 9, 0, 0, 0, time.UTC)
		t3 := base
		t2 := base.Add(3 * time.Hour)
		t1 := base.Add(6 * time.Hour)
		t0 := base.Add(9 * time.Hour)

		commits := []model.CommitPayload{
			commitAt("sha-c0", t0),
			commitAt("sha-c1", t1),
			commitAt("sha-c2", t2),
			commitAt("sha-c3", t3),
		}

		sd := hz.Seeder(owner).Projects(project)
		tmpl := testutil.HB{Project: project, Language: "Go", Editor: "vim", Entity: "a.go", Category: "Coding"}
		// window2 [t3,t2): 5 attributed beats x 60s = 300s.
		w2 := sd.Block(tmpl, t3.Add(10*time.Minute), 5, 60)
		// window1 [t2,t1): deliberately nothing.
		// window0 [t1,t0): 2 attributed beats x 60s = 120s.
		w0 := sd.Block(tmpl, t1.Add(10*time.Minute), 2, 60)
		Expect(w2).To(BeEquivalentTo(300))
		Expect(w0).To(BeEquivalentTo(120))

		users, projects, mins, maxs := stats.CommitGapWindowsForTest(owner, project, commits)
		Expect(mins).To(HaveLen(3), "three consecutive-commit windows")

		got, err := hz.DB.GetTotalTimeBetween(ctx, users, projects, mins, maxs)
		Expect(err).NotTo(HaveOccurred())
		// The core contract: ONE total per input window, in input order, with
		// the heartbeat-free window reported as 0 rather than omitted.
		Expect(got).To(Equal([]int64{120, 0, 300}),
			"want one total per window in input order (window0=120, window1=0, window2=300); "+
				"a short slice means empty windows are still being dropped, a swapped "+
				"slice means the rows came back mis-ordered")

		withTime, err := stats.AttributeCommitSecondsForTest(commits, got)
		Expect(err).NotTo(HaveOccurred())

		secondsFor := func(sha string) int64 {
			cm, ok := withTime[sha]
			ExpectWithOffset(1, ok).To(BeTrue(), "commit %s got no total_seconds at all", sha)
			ExpectWithOffset(1, cm.TotalSeconds).NotTo(BeNil())
			return *cm.TotalSeconds
		}
		Expect(secondsFor("sha-c0")).To(BeEquivalentTo(120), "C0 must keep its own window's 120s")
		Expect(secondsFor("sha-c1")).To(BeEquivalentTo(0), "C1's window has no heartbeats — it must report 0, not a neighbour's time")
		Expect(secondsFor("sha-c2")).To(BeEquivalentTo(300), "C2 must keep its own window's 300s — the tail must not be silently dropped")
		Expect(withTime).NotTo(HaveKey("sha-c3"), "the oldest commit only bounds a window; it closes none")
	})

	It("keeps duplicate windows distinct instead of collapsing them into one aggregate group", func() {
		// The old query GROUPed BY (min_date, max_date), so two commits whose
		// windows share both bounds — an amend/cherry-pick pair with identical
		// author dates, or the same window requested twice — collapsed into a
		// single row and shortened the slice. WITH ORDINALITY keys the group by
		// input position instead.
		hz := testutil.NewHarness(GinkgoT())
		owner, _ := hz.MintUser("commit_attr_dup")
		ctx := context.Background()
		const project = "boomtime"

		base := time.Date(2025, 4, 2, 8, 0, 0, 0, time.UTC)
		lo, hi := base, base.Add(2*time.Hour)

		sd := hz.Seeder(owner).Projects(project)
		total := sd.Block(testutil.HB{Project: project, Entity: "b.go"}, lo.Add(5*time.Minute), 3, 60)
		Expect(total).To(BeEquivalentTo(180))

		got, err := hz.DB.GetTotalTimeBetween(ctx,
			[]string{owner, owner}, []string{project, project},
			[]time.Time{lo, lo}, []time.Time{hi, hi})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]int64{180, 180}), "identical windows must each get their own row")
	})
})
