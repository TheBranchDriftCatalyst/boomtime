// regex_all_aggregations_ginkgo_test.go — ginkgo mirror of regex_all_aggregations_test.go (gaka-0vp.13).
// 1:1 case map (1 stdlib TestXxx → 1 It — massive integration test spans 9 aggregation paths + revert):
//
//	TestRegexRemapAcrossAllAggregations → It "regex rename '^Meet - → Meeting' remaps through every server-side aggregation path"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("regex remap across all aggregations", func() {
	ginkgo.It("a single regex rename '^Meet - → Meeting' remaps concretely through every server-side aggregation path + reverses cleanly", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("rxall")
		_, err := d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		Expect(err).NotTo(HaveOccurred())
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "Meet - A", "Meet - B", "Meet - C", "real-proj")

		const each int64 = 300
		insertRun := func(project, branch, lang, cat, entity string, isWrite bool, startTS time.Time, n int) (int64, int) {
			ins := func(ts time.Time, gap int64) {
				_, err := d.Pool.Exec(ctx, `
					INSERT INTO heartbeats
					  (sender, project, branch, language, category, entity, ty, is_write, time_sent, user_agent, gap_seconds)
					VALUES ($1,$2,$3,$4,$5,$6,'file',$7,$8,'ua',$9)`,
					sender, project, branch, lang, cat, entity, isWrite, ts, gap)
				Expect(err).NotTo(HaveOccurred())
			}
			ins(startTS, 999999) // break
			for i := 0; i < n; i++ {
				ins(startTS.Add(time.Duration(i+1)*time.Minute), each)
			}
			return int64(n) * each, n + 1
		}

		w1 := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC)
		w2 := time.Date(2025, 6, 9, 9, 0, 0, 0, time.UTC)
		w3 := time.Date(2025, 6, 16, 9, 0, 0, 0, time.UTC)

		aW1, aR1 := insertRun("Meet - A", "feature-a", "Go", "Coding", "a.go", true, w1, 2)
		aW2, aR2 := insertRun("Meet - A", "feature-a", "Go", "Coding", "a.go", true, w2, 2)
		tA, rawA := aW1+aW2, aR1+aR2
		tB, rawB := insertRun("Meet - B", "feature-b", "Rust", "Debugging", "b.go", false, w2.Add(time.Hour), 3)
		tC, rawC := insertRun("Meet - C", "feature-c", "Go", "Coding", "c.go", false, w3, 2)
		rW1, rR1 := insertRun("real-proj", "main", "Go", "Coding", "r.go", true, w1.Add(2*time.Hour), 2)
		rW3, rR3 := insertRun("real-proj", "main", "Go", "Coding", "r.go", true, w3.Add(2*time.Hour), 3)
		tR, rawR := rW1+rW3, rR1+rR3

		merged := tA + tB + tC
		grand := merged + tR

		Expect(d.RefreshRollup(ctx, sender, w1.AddDate(0, 0, -1))).To(Succeed())

		start := w1.AddDate(0, 0, -1)
		end := w3.AddDate(0, 0, 7)

		// Baseline.
		rawBefore, err := d.GetUserActivity(ctx, sender, start, end, 30, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		pre := axisTotals(rawBefore, "project")
		Expect(pre["Meet - A"]).To(Equal(tA))
		Expect(pre["Meet - B"]).To(Equal(tB))
		Expect(pre["Meet - C"]).To(Equal(tC))
		Expect(pre["real-proj"]).To(Equal(tR))
		Expect(grandTotal(rawBefore)).To(Equal(grand))

		// Create the regex rule.
		newVal := "Meeting"
		rule, err := d.CreateCurationRule(ctx, sender, "project", "rename", "regex", "^Meet - ", &newVal)
		Expect(err).NotTo(HaveOccurred())
		rs, err := d.LoadRenameSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(rs.HasAxis("project")).To(BeTrue())

		assertMerged := func(label string, rows []StatRow) {
			got := axisTotals(rows, "project")
			for _, raw := range []string{"Meet - A", "Meet - B", "Meet - C"} {
				_, ok := got[raw]
				Expect(ok).To(BeFalse(), "[%s] raw %q still shown after merge", label, raw)
			}
			Expect(got["Meeting"]).To(Equal(merged), "[%s] Meeting", label)
			Expect(got["real-proj"]).To(Equal(tR), "[%s] real-proj untouched", label)
			Expect(grandTotal(rows)).To(Equal(grand), "[%s] grand total conserved", label)
		}

		rawRows, err := d.GetUserActivity(ctx, sender, start, end, 30, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		assertMerged("raw activity", rawRows)

		rollRows, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		assertMerged("rollup", rollRows)

		sumProj := func(rows []ProjectStatRow) int64 {
			var s int64
			for _, r := range rows {
				s += r.TotalSeconds
			}
			return s
		}
		meetingDetail, err := d.GetProjectStats(ctx, sender, "Meeting", start, end, 30, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumProj(meetingDetail)).To(Equal(merged))
		realDetail, err := d.GetProjectStats(ctx, sender, "real-proj", start, end, 30, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumProj(realDetail)).To(Equal(tR))
		emptyRows, err := d.GetProjectStats(ctx, sender, "Meet - A", start, end, 30, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(emptyRows).To(HaveLen(0), "raw source name not addressable under merge")

		projects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var meetingSeen, rawSeen int
		for _, p := range projects {
			switch p {
			case "Meeting":
				meetingSeen++
			case "Meet - A", "Meet - B", "Meet - C":
				rawSeen++
			}
		}
		Expect(meetingSeen).To(Equal(1))
		Expect(rawSeen).To(Equal(0))

		lb, err := d.GetLeaderboards(ctx, start, end, sender, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		lbByProj := map[string]int64{}
		for _, r := range lb {
			if r.Sender == sender {
				lbByProj[r.Project] += r.TotalSeconds
			}
		}
		for _, raw := range []string{"Meet - A", "Meet - B", "Meet - C"} {
			_, ok := lbByProj[raw]
			Expect(ok).To(BeFalse(), "[leaderboards] raw %q still present", raw)
		}
		Expect(lbByProj["Meeting"]).To(Equal(merged))
		Expect(lbByProj["real-proj"]).To(Equal(tR))

		mom, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		momByProj := map[string]int64{}
		momWeeks := map[string]map[string]int64{}
		for _, m := range mom {
			Expect(m.Project).NotTo(Equal("Meet - A"))
			Expect(m.Project).NotTo(Equal("Meet - B"))
			Expect(m.Project).NotTo(Equal("Meet - C"))
			momByProj[m.Project] += m.Seconds
			if momWeeks[m.Project] == nil {
				momWeeks[m.Project] = map[string]int64{}
			}
			momWeeks[m.Project][m.WeekStart.Format("2006-01-02")] += m.Seconds
		}
		Expect(momByProj["Meeting"]).To(Equal(merged))
		Expect(momByProj["real-proj"]).To(Equal(tR))
		mw := momWeeks["Meeting"]
		Expect(len(mw)).To(BeNumerically(">=", 3))
		Expect(mw[w2.Format("2006-01-02")]).To(Equal(int64(600) + tB))

		catTotal := func(rs RenameSets) int64 {
			cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			var s int64
			for _, c := range cats {
				s += c.TotalSeconds
			}
			return s
		}
		catBefore := catTotal(RenameSets{})
		catAfter := catTotal(rs)
		Expect(catAfter).To(Equal(catBefore))
		Expect(catAfter).To(Equal(grand))

		ex, err := d.GetProjectExtras(ctx, sender, "Meeting", start, end, 15, "UTC", rs)
		Expect(err).NotTo(HaveOccurred())
		var exWrite, exRead int64
		for _, e := range ex.Daily {
			exWrite += e.WriteSeconds
			exRead += e.ReadSeconds
		}
		Expect(exWrite).To(Equal(tA))
		Expect(exRead).To(Equal(tB + tC))
		branchTotals := map[string]int64{}
		for _, b := range ex.Branches {
			branchTotals[b.Branch] += b.TotalSeconds
		}
		Expect(branchTotals["feature-a"]).To(Equal(tA))
		Expect(branchTotals["feature-b"]).To(Equal(tB))
		Expect(branchTotals["feature-c"]).To(Equal(tC))
		_, ok := branchTotals["main"]
		Expect(ok).To(BeFalse(), "real-proj's 'main' branch must not appear under Meeting")
		entTotal := int64(0)
		for _, e := range ex.Daily {
			entTotal += e.DistinctEntities
		}
		Expect(entTotal).To(BeNumerically(">=", 3))

		affected, truncated, err := d.CurationAffectedValues(ctx, sender, rule, 200)
		Expect(err).NotTo(HaveOccurred())
		Expect(truncated).To(BeFalse())
		affByVal := map[string]int64{}
		for _, a := range affected {
			affByVal[a.Value] = a.Count
		}
		Expect(affByVal["Meet - A"]).To(Equal(int64(rawA)))
		Expect(affByVal["Meet - B"]).To(Equal(int64(rawB)))
		Expect(affByVal["Meet - C"]).To(Equal(int64(rawC)))
		_, ok = affByVal["real-proj"]
		Expect(ok).To(BeFalse())
		Expect(affected).To(HaveLen(3))

		// Raw preserved in audit surfaces.
		col, _ := ExploreColumn("project")
		groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		groupVals := map[string]bool{}
		for _, g := range groups {
			if g.Value != nil {
				groupVals[*g.Value] = true
			}
		}
		for _, raw := range []string{"Meet - A", "Meet - B", "Meet - C", "real-proj"} {
			Expect(groupVals[raw]).To(BeTrue(), "[audit group] raw %q must still be shown", raw)
		}
		Expect(groupVals["Meeting"]).To(BeFalse(), "[audit group] must NOT show remapped 'Meeting'")

		items, _, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", 1, 1000)
		Expect(err).NotTo(HaveOccurred())
		listVals := map[string]int{}
		for _, r := range items {
			if r.Project != nil {
				listVals[*r.Project]++
			}
		}
		Expect(listVals["Meeting"]).To(Equal(0))

		Expect(rawCountG(d, ctx, sender, "project", "Meet - A")).To(Equal(rawA))
		Expect(rawCountG(d, ctx, sender, "project", "Meet - B")).To(Equal(rawB))
		Expect(rawCountG(d, ctx, sender, "project", "Meet - C")).To(Equal(rawC))
		Expect(rawCountG(d, ctx, sender, "project", "real-proj")).To(Equal(rawR))
		Expect(rawCountG(d, ctx, sender, "project", "Meeting")).To(Equal(0))

		// Reversibility.
		_, err = d.DeleteCurationRule(ctx, sender, rule.ID)
		Expect(err).NotTo(HaveOccurred())
		rs2, err := d.LoadRenameSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(rs2.Any()).To(BeFalse())

		revert, err := d.GetUserActivity(ctx, sender, start, end, 30, "UTC", HiddenSets{}, rs2, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		rv := axisTotals(revert, "project")
		Expect(rv["Meet - A"]).To(Equal(tA))
		Expect(rv["Meet - B"]).To(Equal(tB))
		Expect(rv["Meet - C"]).To(Equal(tC))
		Expect(rv["real-proj"]).To(Equal(tR))
		_, ok = rv["Meeting"]
		Expect(ok).To(BeFalse())

		revRoll, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, rs2, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		rr := axisTotals(revRoll, "project")
		Expect(rr["Meet - A"]).To(Equal(tA))
		Expect(rr["Meeting"]).To(BeEquivalentTo(0))
		revProjects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{}, rs2, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var revMeet, revMeeting int
		for _, p := range revProjects {
			if p == "Meeting" {
				revMeeting++
			}
			if p == "Meet - A" || p == "Meet - B" || p == "Meet - C" {
				revMeet++
			}
		}
		Expect(revMeet).To(Equal(3))
		Expect(revMeeting).To(Equal(0))

		Expect(grandTotal(rawBefore)).To(Equal(grandTotal(rawRows)))
		Expect(grandTotal(rawRows)).To(Equal(grandTotal(revert)))
	})
})
