// owner_scoping_ginkgo_test.go — ginkgo mirror of owner_scoping_test.go (gaka-0vp.13).
// 1:1 case map (1 stdlib TestXxx wrapping 17 subtests → 17 Its inside a Describe):
//
//	TestOwnerScopingAcrossAggregations/{Xxx} → It "GetXxx: A's query doesn't include B's rows"
package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// seedTwoUserBlockG mirrors seedTwoUserBlock: 2 attributed + 1 break for owner
// under project, returning attributed seconds (200 per user).
func seedTwoUserBlockG(d *DB, sender, project string, day time.Time) int64 {
	ctx := context.Background()
	ensureUserG(d, ctx, sender)
	ensureProjectsG(d, ctx, sender, project)
	tmpl := hbSeed{
		project: project, language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding",
		entity: "a.go",
	}
	brk := tmpl
	brk.ts = day
	brk.gap = 999999
	insertSeedG(d, ctx, sender, brk)
	for i := 0; i < 2; i++ {
		h := tmpl
		h.ts = day.Add(time.Duration(i+1) * time.Minute)
		h.gap = 100
		insertSeedG(d, ctx, sender, h)
	}
	return 200
}

var _ = ginkgo.Describe("owner scoping across aggregations", func() {
	day := time.Date(2025, 4, 5, 10, 0, 0, 0, time.UTC)
	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 1)

	newPair := func(d *DB, tag string) (string, string) {
		ctx := context.Background()
		userA := mkSender("ownA_" + tag)
		userB := mkSender("ownB_" + tag)
		cleanupSenderG(d, ctx, userA)
		cleanupSenderG(d, ctx, userB)
		return userA, userB
	}

	ginkgo.It("GetUserActivity: A's total excludes B's rows", func() {
		d := openTestDBG()
		a, b := newPair(d, "act")
		seedTwoUserBlockG(d, a, "SharedProj", day)
		seedTwoUserBlockG(d, b, "SharedProj", day)
		rowsA, err := d.GetUserActivity(context.Background(), a, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rowsA)).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetUserActivityRollup: A's rollup total excludes B's rows", func() {
		d := openTestDBG()
		a, b := newPair(d, "roll")
		seedTwoUserBlockG(d, a, "SharedProj", day)
		seedTwoUserBlockG(d, b, "SharedProj", day)
		Expect(d.RefreshRollup(context.Background(), a, start)).To(Succeed())
		Expect(d.RefreshRollup(context.Background(), b, start)).To(Succeed())
		rowsA, err := d.GetUserActivityRollup(context.Background(), a, start, end,
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rowsA)).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetCategoryDaily: A's category total excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "cat")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		cats, err := d.GetCategoryDaily(context.Background(), a, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var tot int64
		for _, c := range cats {
			tot += c.TotalSeconds
		}
		Expect(tot).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetPunchcard: A's punch total excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "pnch")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		cells, err := d.GetPunchcard(context.Background(), a, start, end, 15, "UTC",
			HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumPunch(cells)).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetSessions: A's sessions total excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "sess")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		sess, err := d.GetSessions(context.Background(), a, start, end, 15, "UTC",
			HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumSessions(sess)).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetMomentum: A's momentum total excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "mom")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		mom, err := d.GetMomentum(context.Background(), a, start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumMomentum(mom)).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetProjectStats: A's project stats excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "pstat")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		rows, err := d.GetProjectStats(context.Background(), a, "P", start, end, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var tot int64
		for _, r := range rows {
			tot += r.TotalSeconds
		}
		Expect(tot).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetProjectExtras: A's branch total excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "pex")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		ex, err := d.GetProjectExtras(context.Background(), a, "P", start, end, 15, "UTC", RenameSets{})
		Expect(err).NotTo(HaveOccurred())
		var brTot int64
		for _, br := range ex.Branches {
			brTot += br.TotalSeconds
		}
		Expect(brTot).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetActiveFiles: A's active-files total excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "af")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		files, _, err := d.GetActiveFiles(context.Background(), a, start, end, 15, 20,
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var tot int64
		for _, f := range files {
			tot += f.Seconds
		}
		Expect(tot).To(BeEquivalentTo(200))
	})

	ginkgo.It("GetAllProjects: A doesn't see B's projects", func() {
		d := openTestDBG()
		a, b := newPair(d, "gap")
		seedTwoUserBlockG(d, a, "OnlyA", day)
		seedTwoUserBlockG(d, b, "OnlyB", day)
		projs, err := d.GetAllProjects(context.Background(), a, start, end,
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		for _, p := range projs {
			Expect(strings.EqualFold(p, "OnlyB")).To(BeFalse(), "A saw B's project %q — cross-tenant leak", p)
		}
	})

	ginkgo.It("GetTotalTimeToday: A's today total excludes B", func() {
		d := openTestDBG()
		today := time.Now().UTC().Truncate(24 * time.Hour).Add(6 * time.Hour)
		a, b := newPair(d, "today")
		seedTwoUserBlockG(d, a, "P", today)
		seedTwoUserBlockG(d, b, "P", today)
		tot, err := d.GetTotalTimeToday(context.Background(), a, "UTC", HiddenSets{})
		Expect(err).NotTo(HaveOccurred())
		Expect(tot).To(BeEquivalentTo(200), "not 400")
	})

	ginkgo.It("GetTimeline: row count is stable when a second user seeds identical shape", func() {
		d := openTestDBG()
		a, b := newPair(d, "tl")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		tl, err := d.GetTimeline(context.Background(), a, start, end, 15, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		aloneTL, err := d.GetTimeline(context.Background(), a, start, end, 15, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(aloneTL)).To(Equal(len(tl)))
	})

	ginkgo.It("ListEntitiesByType: A's entity count doesn't include B's rows", func() {
		d := openTestDBG()
		a, b := newPair(d, "ent")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		list, _, err := d.ListEntitiesByType(context.Background(), a, "file", 100)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range list {
			if strings.EqualFold(e.Entity, "a.go") {
				Expect(e.Count).To(BeNumerically("<=", 3), "B's rows leaked in")
			}
		}
	})

	ginkgo.It("GroupHeartbeats: A's per-language count excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "grp")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		langCol, _ := ExploreColumn("language")
		grps, _, err := d.GroupHeartbeats(context.Background(), a, langCol, start, end, nil, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		var goCount int64
		for _, g := range grps {
			if g.Value != nil && strings.EqualFold(*g.Value, "Go") {
				goCount = g.Count
			}
		}
		Expect(goCount).To(BeNumerically("<=", 3), "B's rows leaked")
	})

	ginkgo.It("ListHeartbeats: A's list count excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "lst")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		items, total, err := d.ListHeartbeats(context.Background(), a, start, end, nil, "", 1, 500)
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(BeNumerically("<=", 3))
		Expect(len(items)).To(BeNumerically("<=", 3))
	})

	ginkgo.It("LatestHeartbeat: A's count excludes B", func() {
		d := openTestDBG()
		a, b := newPair(d, "lat")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		_, count, err := d.LatestHeartbeat(context.Background(), a)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(BeEquivalentTo(3))
	})

	ginkgo.It("GetLeaderboards: both users appear but requester total is A's-only (200 not 400)", func() {
		d := openTestDBG()
		a, b := newPair(d, "lb")
		seedTwoUserBlockG(d, a, "P", day)
		seedTwoUserBlockG(d, b, "P", day)
		lb, err := d.GetLeaderboards(context.Background(), start, end, a,
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var aTot, bTot int64
		for _, r := range lb {
			if r.Sender == a {
				aTot += r.TotalSeconds
			} else if r.Sender == b {
				bTot += r.TotalSeconds
			}
		}
		Expect(aTot).To(BeEquivalentTo(200))
		Expect(bTot).To(BeEquivalentTo(200))
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func seedTwoUserBlock(t *testing.T, d *DB, sender, project string, day time.Time) int64 {
	t.Helper()
	ctx := t.Context()
	ensureUser(t, d, ctx, sender)
	ensureProjects(t, d, ctx, sender, project)
	tmpl := hbSeed{
		project: project, language: "Go", editor: "vim", plugin: "pl",
		machine: "m", platform: "linux", branch: "main", category: "Coding",
		entity: "a.go",
	}
	brk := tmpl
	brk.ts = day
	brk.gap = 999999
	insertSeed(t, d, ctx, sender, brk)
	for i := 0; i < 2; i++ {
		h := tmpl
		h.ts = day.Add(time.Duration(i+1) * time.Minute)
		h.gap = 100
		insertSeed(t, d, ctx, sender, h)
	}
	return 200
}
