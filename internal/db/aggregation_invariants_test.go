// aggregation_invariants_ginkgo_test.go — ginkgo mirror of aggregation_invariants_test.go (gaka-0vp.13).
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//
//	TestCategoryDailyPerDaySumsMatchGrandTotal   → It "CategoryDaily per-day sums match grand total + cross-checks GetUserActivity"
//	TestTotalTimeBetweenReturnsAscendingSums     → It "GetTotalTimeBetween: per-window sums correct + tenant-isolated"
//	TestListHeartbeatsPagesArePartitioned        → It "ListHeartbeats pages partition rows (no dups, no drops)"
//	TestActiveFilesCaseVariantProjectDistinctCount → It "ActiveFiles case-variant project DISTINCT count folds to one"
package db

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("aggregation invariants (gaka-oew)", func() {
	ginkgo.It("CategoryDaily per-day sums match the grand total, and cross-check GetUserActivity", func() {
		d := openTestDBG()
		f := newSenderG(d, "catgrand")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		day1 := time.Date(2025, 6, 10, 10, 0, 0, 0, time.UTC)
		day2 := day1.AddDate(0, 0, 1)

		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Coding", ts: day1, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Coding", ts: day1.Add(time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Coding", ts: day1.Add(2 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "b.go", category: "Debugging", ts: day1.Add(3 * time.Minute), gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "b.go", category: "Debugging", ts: day1.Add(4 * time.Minute), gap: 100})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Coding", ts: day2, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", category: "Coding", ts: day2.Add(time.Minute), gap: 100})

		start := day1.AddDate(0, 0, -1)
		end := day2.AddDate(0, 0, 1)

		cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		byDay := map[string]int64{}
		var grand int64
		for _, c := range cats {
			byDay[c.Day.Format("2006-01-02")] += c.TotalSeconds
			grand += c.TotalSeconds
		}
		Expect(byDay[day1.Format("2006-01-02")]).To(BeEquivalentTo(300))
		Expect(byDay[day2.Format("2006-01-02")]).To(BeEquivalentTo(100))
		Expect(grand).To(BeEquivalentTo(400))

		act, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(act)).To(Equal(grand))
	})

	ginkgo.It("GetTotalTimeBetween: per-window sums correct and tenant-isolated (regression: gaka-6yr unnest ambiguity)", func() {
		d := openTestDBG()
		f := newSenderG(d, "ttbtwn")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		base := time.Date(2025, 6, 15, 9, 0, 0, 0, time.UTC)
		w1Start := base
		w1End := base.Add(30 * time.Minute)
		w2Start := base.Add(1 * time.Hour)
		w2End := base.Add(90 * time.Minute)
		w3Start := base.Add(2 * time.Hour)
		w3End := base.Add(150 * time.Minute)

		insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w1Start.Add(time.Minute), gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w1Start.Add(2 * time.Minute), gap: 60})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w1Start.Add(3 * time.Minute), gap: 60})
		insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w2Start.Add(time.Minute), gap: 999999})
		for i := 0; i < 5; i++ {
			insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w2Start.Add(time.Duration(i+2) * time.Minute), gap: 60})
		}
		insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w3Start.Add(time.Minute), gap: 999999})
		for i := 0; i < 3; i++ {
			insertSeedG(d, ctx, sender, hbSeed{project: "P", ts: w3Start.Add(time.Duration(i+2) * time.Minute), gap: 60})
		}

		other := newSenderG(d, "ttbtwn-other")
		other.Projects("P")
		insertSeedG(d, ctx, other.Sender(), hbSeed{project: "P", ts: w1Start.Add(time.Minute), gap: 999999})
		for i := 0; i < 30; i++ {
			insertSeedG(d, ctx, other.Sender(), hbSeed{project: "P", ts: w1Start.Add(time.Duration(i+2) * time.Second), gap: 60})
		}

		users := []string{sender, sender, sender}
		projects := []string{"P", "P", "P"}
		mins := []time.Time{w3Start, w2Start, w1Start}
		maxs := []time.Time{w3End, w2End, w1End}

		got, err := d.GetTotalTimeBetween(ctx, users, projects, mins, maxs)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))

		var sum int64
		for _, v := range got {
			sum += v
		}
		Expect(sum).To(BeEquivalentTo(120 + 300 + 180))

		counts := map[int64]int{120: 0, 300: 0, 180: 0}
		for _, v := range got {
			counts[v]++
		}
		for wantT, n := range counts {
			Expect(n).To(Equal(1), "window total %d appeared %d times, want 1", wantT, n)
		}
		for _, v := range got {
			Expect(v == 120 || v == 300 || v == 180).To(BeTrue(),
				"unexpected window total %d — likely tenant-scope leak", v)
		}
	})

	ginkgo.It("ListHeartbeats pages partition rows (no dups, no drops) with total=5", func() {
		d := openTestDBG()
		f := newSenderG(d, "lhpg")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		day := time.Date(2025, 6, 25, 10, 0, 0, 0, time.UTC)
		entities := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
		for i, ent := range entities {
			insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: ent,
				ts: day.Add(time.Duration(i) * time.Minute), gap: 60})
		}

		start := day.AddDate(0, 0, -1)
		end := day.AddDate(0, 0, 1)

		seen := map[int64]int{}
		var totalSeen int
		for page := 1; page <= 3; page++ {
			items, total, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", page, 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(BeEquivalentTo(5))
			for _, r := range items {
				seen[r.ID]++
				totalSeen++
			}
		}
		Expect(totalSeen).To(Equal(5))
		for id, n := range seen {
			Expect(n).To(Equal(1), "row id %d appeared %d times across pages", id, n)
		}
		Expect(seen).To(HaveLen(5))
	})

	ginkgo.It("ActiveFiles case-variant project DISTINCT count folds to one (via lower(project))", func() {
		d := openTestDBG()
		f := newSenderG(d, "afcvp")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("MyProject", "myproject")

		day := time.Date(2025, 6, 28, 10, 0, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "MyProject", entity: "shared.go", ts: day, gap: 60})
		insertSeedG(d, ctx, sender, hbSeed{project: "myproject", entity: "shared.go", ts: day.Add(time.Minute), gap: 60})

		files, _, err := d.GetActiveFiles(ctx, sender, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1),
			15, 20, HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var found bool
		for _, af := range files {
			if af.Entity == "shared.go" || af.Entity == "SHARED.GO" || af.Entity == "Shared.go" {
				found = true
				Expect(af.Projects).To(BeEquivalentTo(1), "case-variant project names must fold to one")
				Expect(af.Seconds).To(BeEquivalentTo(120))
			}
		}
		Expect(found).To(BeTrue(), "shared.go not in active-files output")
	})
})
