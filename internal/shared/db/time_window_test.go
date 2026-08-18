// time_window_ginkgo_test.go — ginkgo mirror of time_window_test.go (gaka-0vp.13).
// 1:1 case map (3 stdlib TestXxx → 3 Its):
//
//	TestTimeWindowInclusiveBothEdges   → "inclusive [start,end] on 5 aggregations"
//	TestTimeWindowExcludesOutside      → "out-of-window rows dropped"
//	TestTimeWindowTimelineExclusiveEnd → "GetTimeline: half-open [start,end)"
package db

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("time window boundaries", func() {
	ginkgo.It("aggregations use inclusive [start,end] on both edges", func() {
		d := openTestDBG()
		f := newSenderG(d, "twin_incl")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		// Range spanning exactly [start, end]. Seed a break + 2 attributed.
		start := time.Date(2025, 4, 10, 8, 0, 0, 0, time.UTC)
		end := time.Date(2025, 4, 10, 12, 0, 0, 0, time.UTC)

		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: start, gap: 999999}) // break at start edge
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: start.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: end, gap: 100}) // exactly at end edge

		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rows)).To(BeEquivalentTo(200), "[GetUserActivity] both edges must be inclusive")

		cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var catTot int64
		for _, c := range cats {
			catTot += c.TotalSeconds
		}
		Expect(catTot).To(BeEquivalentTo(200), "[GetCategoryDaily] both edges inclusive")

		punch, err := d.GetPunchcard(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumPunch(punch)).To(BeEquivalentTo(200), "[GetPunchcard] both edges inclusive")

		sess, err := d.GetSessions(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumSessions(sess)).To(BeEquivalentTo(200), "[GetSessions] both edges inclusive")

		mom, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumMomentum(mom)).To(BeEquivalentTo(200), "[GetMomentum] both edges inclusive")
	})

	ginkgo.It("heartbeats one second outside [start,end] are excluded", func() {
		d := openTestDBG()
		f := newSenderG(d, "twin_excl")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		start := time.Date(2025, 4, 11, 8, 0, 0, 0, time.UTC)
		end := time.Date(2025, 4, 11, 12, 0, 0, 0, time.UTC)

		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: start.Add(-time.Second), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: start, gap: 999999}) // break
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: start.Add(time.Hour), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go",
			category: "Coding", ts: end.Add(time.Second), gap: 100})

		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rows)).To(BeEquivalentTo(100), "out-of-window rows must be dropped")
	})

	ginkgo.It("GetTimeline uses half-open [start,end) — end-boundary row does NOT count", func() {
		d := openTestDBG()
		f := newSenderG(d, "twin_tl")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		start := time.Date(2025, 4, 12, 8, 0, 0, 0, time.UTC)
		end := time.Date(2025, 4, 12, 12, 0, 0, 0, time.UTC)

		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "a.go",
			ts: start, gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "a.go",
			ts: start.Add(time.Hour), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", language: "Go", entity: "a.go",
			ts: end, gap: 100})

		tl, err := d.GetTimeline(ctx, sender, start, end, 15, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())

		tl2, err := d.GetTimeline(ctx, sender, start, end.Add(time.Second), 15, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())

		Expect(len(tl)).NotTo(Equal(len(tl2)),
			"timeline end edge is inclusive (rows same for [start,end)=%d and [start,end+1s)=%d) — must be exclusive per get_timeline.sql (`time_sent < $3`)",
			len(tl), len(tl2))
	})
})
