// rename_merge_ginkgo_test.go — ginkgo mirror of rename_merge_test.go (boom-0vp.13).
// 1:1 case map (15 stdlib TestXxx incl 3 subtests → 14 Its + 1 DescribeTable(3)):
//
//	TestRenameRawPreservation             → It "rename mutates no raw data"
//	TestRenameMergeAggregates             → DescribeTable "merge aggregates per axis" (3 axes)
//	TestRenameRollupMerge                 → It "rollup fast path merges A,B -> M"
//	TestRenameReversibility               → It "deleting rule reverts dashboards to raw"
//	TestRenameIngestStoresRaw             → It "ingest stores raw values under active rename"
//	TestRenameProjectDetailByDisplayName  → It "GetProjectStats keys by display name"
//	TestRenameProjectListMerge            → It "project list shows merged name once"
//	TestRenameAuditUnaffected             → It "audit surfaces show raw values, not remapped"
//	TestRenameHidePrecedence              → It "hide precedence: A hidden, only B merges into M"
//	TestRenameLeaderboardRequesterOnly    → It "requester rename only affects requester rows"
//	TestRenameMomentumAndCategory         → It "momentum + category-daily merge"
//	TestRegexRenameMerge                  → It "regex rename merges all Meet* projects"
//	TestCurationAffectedValues            → It "affected values: regex + exact"
//	TestRenameProjectExtras               → It "extras aggregates through project + branch rename"
//	TestCheckProjectDisplayOwner          → It "display-name owner check resolves through remap"
package db

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("rename merge", func() {
	ginkgo.It("creating a rename rule mutates no raw data (heartbeats/projects/badges/rollup preserved)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renraw")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(30*time.Minute), 1, 100)
		_, err := d.CreateBadgeLink(ctx, sender, "A")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1))).To(Succeed())

		rawABefore := rawCountG(d, ctx, sender, "project", "A")
		createRenameG(d, ctx, sender, "project", "A", "M")

		Expect(rawCountG(d, ctx, sender, "project", "A")).To(Equal(rawABefore))
		Expect(rawABefore).NotTo(Equal(0))
		Expect(rawCountG(d, ctx, sender, "project", "M")).To(Equal(0))
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM projects WHERE owner=$1 AND name='A'`, sender)).To(Equal(1))
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM projects WHERE owner=$1 AND name='M'`, sender)).To(Equal(0))
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM badges WHERE username=$1 AND project='A'`, sender)).To(Equal(1))
		Expect(scalarCountG(d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1 AND project='A'`, sender)).NotTo(Equal(0))
	})

	ginkgo.DescribeTable("A,B,C -> M merges in aggregations (per axis)",
		func(axis string) {
			d := openTestDBG()
			ctx := context.Background()

			sender := mkSender("renmrg_" + axis)
			_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
			cleanupSenderG(d, ctx, sender)
			if axis == "project" {
				ensureProjectsG(d, ctx, sender, "A", "B", "C")
			} else {
				ensureProjectsG(d, ctx, sender, "P")
			}

			day := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC)
			tA, _ := seedAxisBlockG(d, ctx, sender, axis, "A", day, 2, 100)
			tB, _ := seedAxisBlockG(d, ctx, sender, axis, "B", day.Add(20*time.Minute), 3, 100)
			tC, _ := seedAxisBlockG(d, ctx, sender, axis, "C", day.Add(40*time.Minute), 1, 100)
			total := tA + tB + tC

			start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

			before, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(grandTotal(before)).To(Equal(total))

			for _, v := range []string{"A", "B", "C"} {
				createRenameG(d, ctx, sender, axis, v, "M")
			}
			rs := loadRenamesG(d, ctx, sender)

			after, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			secs := axisTotals(after, axis)
			for _, v := range []string{"A", "B", "C"} {
				_, ok := secs[v]
				Expect(ok).To(BeFalse(), "[%s] %s still shown after merge", axis, v)
			}
			Expect(secs["M"]).To(Equal(total))
			Expect(grandTotal(after)).To(Equal(total))

			var pctSum float64
			for _, r := range after {
				pctSum += r.Pct
			}
			Expect(pctSum).To(BeNumerically(">=", 0.999))
			Expect(pctSum).To(BeNumerically("<=", 1.001))
		},
		ginkgo.Entry("project axis", "project"),
		ginkgo.Entry("language axis", "language"),
		ginkgo.Entry("editor axis", "editor"),
	)

	ginkgo.It("rollup fast path merges A,B -> M (rt[A]=rt[B]=0, rt[M]=tA+tB)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renroll")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 3, 9, 0, 0, 0, time.UTC)
		tA, _ := seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		tB, _ := seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(30*time.Minute), 1, 100)
		Expect(d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1))).To(Succeed())
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "A", "M")
		createRenameG(d, ctx, sender, "project", "B", "M")
		rs := loadRenamesG(d, ctx, sender)

		roll, err := d.GetUserActivityRollup(ctx, sender, start, end, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		rt := axisTotals(roll, "project")
		Expect(rt["A"]).To(BeEquivalentTo(0))
		Expect(rt["B"]).To(BeEquivalentTo(0))
		Expect(rt["M"]).To(Equal(tA + tB))
	})

	ginkgo.It("deleting the rule instantly reverts dashboards to raw", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renrev")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 4, 9, 0, 0, 0, time.UTC)
		tA, _ := seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		tB, _ := seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(30*time.Minute), 1, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		ruleID := createRenameG(d, ctx, sender, "project", "A", "B")
		rs := loadRenamesG(d, ctx, sender)
		merged, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		ms := axisTotals(merged, "project")
		Expect(ms["B"]).To(Equal(tA + tB))
		Expect(ms["A"]).To(BeEquivalentTo(0))

		_, err = d.DeleteCurationRule(ctx, sender, ruleID)
		Expect(err).NotTo(HaveOccurred())
		rs = loadRenamesG(d, ctx, sender)
		Expect(rs.Any()).To(BeFalse())
		reverted, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		rv := axisTotals(reverted, "project")
		Expect(rv["A"]).To(Equal(tA))
		Expect(rv["B"]).To(Equal(tB))
	})

	ginkgo.It("ingest stores heartbeats RAW under an active rename; aggregates under target at read time", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("rening")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "P")

		day := time.Date(2025, 6, 5, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, sender, "language", "M", day, 1, 100)
		createRenameG(d, ctx, sender, "language", "A", "M")

		ptr := func(s string) *string { return &s }
		proj := "P"
		beats := []model.HeartbeatPayload{
			{Sender: ptr(sender), Project: &proj, Language: ptr("A"), Entity: "n.go", Type: model.FileType, TimeSent: float64(day.Add(2 * time.Hour).Unix()), UserAgent: "ua"},
			{Sender: ptr(sender), Project: &proj, Language: ptr("A"), Entity: "n.go", Type: model.FileType, TimeSent: float64(day.Add(2*time.Hour + time.Minute).Unix()), UserAgent: "ua"},
		}
		_, err := d.SaveHeartbeats(ctx, beats)
		Expect(err).NotTo(HaveOccurred())

		Expect(rawCountG(d, ctx, sender, "language", "A")).To(Equal(2), "ingest should store raw 'A'")

		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)
		rs := loadRenamesG(d, ctx, sender)
		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		secs := axisTotals(rows, "language")
		_, ok := secs["A"]
		Expect(ok).To(BeFalse())
		Expect(secs["M"]).To(BeNumerically(">", 100))
	})

	ginkgo.It("GetProjectStats keyed by display name aggregates source projects (identity works too)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("rendetail")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B", "keep")

		day := time.Date(2025, 6, 6, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100)
		seedAxisBlockG(d, ctx, sender, "project", "keep", day.Add(40*time.Minute), 1, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "A", "M")
		createRenameG(d, ctx, sender, "project", "B", "M")
		rs := loadRenamesG(d, ctx, sender)

		sumRows := func(rows []ProjectStatRow) int64 {
			var s int64
			for _, r := range rows {
				s += r.TotalSeconds
			}
			return s
		}

		rowsM, err := d.GetProjectStats(ctx, sender, "M", start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumRows(rowsM)).To(BeEquivalentTo(300))

		rowsKeep, err := d.GetProjectStats(ctx, sender, "keep", start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumRows(rowsKeep)).To(BeEquivalentTo(100))

		rowsA, err := d.GetProjectStats(ctx, sender, "A", start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(rowsA).To(HaveLen(0))
	})

	ginkgo.It("projects list shows the merged name once (no raw A/B)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renlist")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 7, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "A", "M")
		createRenameG(d, ctx, sender, "project", "B", "M")
		rs := loadRenamesG(d, ctx, sender)

		projects, err := d.GetAllProjects(ctx, sender, start, end, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var mCount, abCount int
		for _, p := range projects {
			switch p {
			case "M":
				mCount++
			case "A", "B":
				abCount++
			}
		}
		Expect(mCount).To(Equal(1))
		Expect(abCount).To(Equal(0))
	})

	ginkgo.It("audit surfaces (group + list) show raw values even with a rule active", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renaudit")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 8, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(20*time.Minute), 1, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "A", "M")
		createRenameG(d, ctx, sender, "project", "B", "M")

		col, _ := ExploreColumn("project")
		groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		hasVal := func(v string) bool {
			for _, g := range groups {
				if g.Value != nil && *g.Value == v {
					return true
				}
			}
			return false
		}
		Expect(hasVal("A")).To(BeTrue())
		Expect(hasVal("B")).To(BeTrue())
		Expect(hasVal("M")).To(BeFalse())

		items, _, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", 1, 500)
		Expect(err).NotTo(HaveOccurred())
		var sawA bool
		for _, r := range items {
			if r.Project != nil && *r.Project == "A" {
				sawA = true
			}
			if r.Project != nil {
				Expect(*r.Project).NotTo(Equal("M"), "audit list must not show remapped 'M'")
			}
		}
		Expect(sawA).To(BeTrue())
	})

	ginkgo.It("hide precedence: A hidden, A,B -> M merges only B into M", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renhide")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 9, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, sender, "project", "A", day, 2, 100)
		tB, _ := seedAxisBlockG(d, ctx, sender, "project", "B", day.Add(20*time.Minute), 3, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "A", "M")
		createRenameG(d, ctx, sender, "project", "B", "M")
		_, err := d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "A", nil)
		Expect(err).NotTo(HaveOccurred())
		hs, err := d.LoadHiddenSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		rs := loadRenamesG(d, ctx, sender)

		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", hs, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		secs := axisTotals(rows, "project")
		Expect(secs["M"]).To(Equal(tB))
		_, ok := secs["A"]
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("requester's rename applies to their rows only; other user's project labels untouched", func() {
		d := openTestDBG()
		ctx := context.Background()

		me := mkSender("renlbme")
		other := mkSender("renlboth")
		for _, s := range []string{me, other} {
			_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, s)
			ensureProjectsG(d, ctx, s, "A")
		}
		ginkgo.DeferCleanup(func() {
			for _, s := range []string{me, other} {
				_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, s)
				_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, s)
				_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, s)
				_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, s)
				_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, s)
			}
		})

		day := time.Date(2025, 6, 10, 9, 0, 0, 0, time.UTC)
		seedAxisBlockG(d, ctx, me, "project", "A", day, 2, 100)
		seedAxisBlockG(d, ctx, other, "project", "A", day, 2, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, me, "project", "A", "M")
		rs := loadRenamesG(d, ctx, me)

		lb, err := d.GetLeaderboards(ctx, start, end, me, HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var meProj, otherProj string
		for _, r := range lb {
			switch r.Sender {
			case me:
				meProj = r.Project
			case other:
				otherProj = r.Project
			}
		}
		Expect(meProj).To(Equal("M"))
		Expect(otherProj).To(Equal("A"))
	})

	ginkgo.It("momentum + category-daily aggregations both merge under renames", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renmomcat")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "A", "B")

		day := time.Date(2025, 6, 11, 9, 0, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "A", language: "Go", category: "X", entity: "a.go", ts: day, gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "A", language: "Go", category: "X", entity: "a.go", ts: day.Add(time.Minute), gap: 120})
		insertSeedG(d, ctx, sender, hbSeed{project: "B", language: "Go", category: "Y", entity: "b.go", ts: day.Add(2 * time.Minute), gap: 999999})
		insertSeedG(d, ctx, sender, hbSeed{project: "B", language: "Go", category: "Y", entity: "b.go", ts: day.Add(3 * time.Minute), gap: 120})
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRenameG(d, ctx, sender, "project", "A", "M")
		createRenameG(d, ctx, sender, "project", "B", "M")
		createRenameG(d, ctx, sender, "category", "X", "Z")
		createRenameG(d, ctx, sender, "category", "Y", "Z")
		rs := loadRenamesG(d, ctx, sender)

		mom, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var momM int64
		for _, m := range mom {
			Expect(m.Project).NotTo(Equal("A"))
			Expect(m.Project).NotTo(Equal("B"))
			if m.Project == "M" {
				momM += m.Seconds
			}
		}
		Expect(momM).To(BeEquivalentTo(240))

		cats, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		var catZ int64
		for _, c := range cats {
			Expect(c.Category).NotTo(Equal("X"))
			Expect(c.Category).NotTo(Equal("Y"))
			if c.Category == "Z" {
				catZ += c.TotalSeconds
			}
		}
		Expect(catZ).To(BeEquivalentTo(240))
	})

	ginkgo.It("regex rename '^Meet -> Meeting' merges all Meet* projects (raw untouched + reversible)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renrx")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "Meet - Standup", "Meet - Planning", "Meet - Retro", "real-project")

		day := time.Date(2025, 6, 12, 9, 0, 0, 0, time.UTC)
		t1, _ := seedAxisBlockG(d, ctx, sender, "project", "Meet - Standup", day, 2, 100)
		t2, _ := seedAxisBlockG(d, ctx, sender, "project", "Meet - Planning", day.Add(20*time.Minute), 3, 100)
		t3, _ := seedAxisBlockG(d, ctx, sender, "project", "Meet - Retro", day.Add(40*time.Minute), 1, 100)
		tk, _ := seedAxisBlockG(d, ctx, sender, "project", "real-project", day.Add(60*time.Minute), 2, 100)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		ruleID := createRegexRenameG(d, ctx, sender, "project", "^Meet", "Meeting")
		rs := loadRenamesG(d, ctx, sender)
		Expect(rs.HasAxis("project")).To(BeTrue())

		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		secs := axisTotals(rows, "project")
		for _, v := range []string{"Meet - Standup", "Meet - Planning", "Meet - Retro"} {
			_, ok := secs[v]
			Expect(ok).To(BeFalse(), "regex merge still shows raw %q", v)
		}
		Expect(secs["Meeting"]).To(Equal(t1 + t2 + t3))
		Expect(secs["real-project"]).To(Equal(tk))

		Expect(rawCountG(d, ctx, sender, "project", "Meet - Standup")).NotTo(Equal(0))
		Expect(rawCountG(d, ctx, sender, "project", "Meeting")).To(Equal(0))

		_, err = d.DeleteCurationRule(ctx, sender, ruleID)
		Expect(err).NotTo(HaveOccurred())
		rs = loadRenamesG(d, ctx, sender)
		reverted, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, rs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		rv := axisTotals(reverted, "project")
		Expect(rv["Meet - Standup"]).To(Equal(t1))
		Expect(rv["Meet - Planning"]).To(Equal(t2))
		Expect(rv["Meet - Retro"]).To(Equal(t3))
		_, ok := rv["Meeting"]
		Expect(ok).To(BeFalse())
	})

	ginkgo.It("CurationAffectedValues: regex returns matching raw values with counts (desc); exact returns single value", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renaff")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "Meet - Standup", "Meet - Planning", "real-project")

		day := time.Date(2025, 6, 13, 9, 0, 0, 0, time.UTC)
		for i := 0; i < 3; i++ {
			insertSeedG(d, ctx, sender, hbSeed{project: "Meet - Standup", language: "Go", entity: "a.go", ts: day.Add(time.Duration(i) * time.Minute), gap: 60})
		}
		for i := 0; i < 2; i++ {
			insertSeedG(d, ctx, sender, hbSeed{project: "Meet - Planning", language: "Go", entity: "b.go", ts: day.Add(time.Duration(10+i) * time.Minute), gap: 60})
		}
		insertSeedG(d, ctx, sender, hbSeed{project: "real-project", language: "Go", entity: "c.go", ts: day.Add(30 * time.Minute), gap: 60})

		rxID := createRegexRenameG(d, ctx, sender, "project", "^Meet", "Meeting")
		rxRule, _, err := d.GetCurationRule(ctx, rxID)
		Expect(err).NotTo(HaveOccurred())
		vals, trunc, err := d.CurationAffectedValues(ctx, sender, rxRule, 200)
		Expect(err).NotTo(HaveOccurred())
		Expect(trunc).To(BeFalse())
		byVal := map[string]int64{}
		for _, v := range vals {
			byVal[v.Value] = v.Count
		}
		Expect(byVal["Meet - Standup"]).To(BeEquivalentTo(3))
		Expect(byVal["Meet - Planning"]).To(BeEquivalentTo(2))
		_, ok := byVal["real-project"]
		Expect(ok).To(BeFalse())
		Expect(vals).To(HaveLen(2))
		Expect(vals[0].Value).To(Equal("Meet - Standup"), "count-desc order")

		exID := createRenameG(d, ctx, sender, "project", "real-project", "Misc")
		exRule, _, err := d.GetCurationRule(ctx, exID)
		Expect(err).NotTo(HaveOccurred())
		evals, _, err := d.CurationAffectedValues(ctx, sender, exRule, 200)
		Expect(err).NotTo(HaveOccurred())
		Expect(evals).To(HaveLen(1))
		Expect(evals[0].Value).To(Equal("real-project"))
		Expect(evals[0].Count).To(BeEquivalentTo(1))
	})

	ginkgo.It("GetProjectExtras aggregates through project + branch renames (raw untouched)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("renext")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "Meet - A", "Meet - B", "Meeting")

		day := time.Date(2025, 6, 14, 9, 0, 0, 0, time.UTC)
		insertFile := func(project, branch, entity string, isWrite bool, ts time.Time, gap int64) {
			_, err := d.Pool.Exec(ctx, `
				INSERT INTO heartbeats (sender, project, branch, entity, ty, is_write, time_sent, user_agent, gap_seconds)
				VALUES ($1,$2,$3,$4,'file',$5,$6,'ua',$7)`,
				sender, project, branch, entity, isWrite, ts, gap)
			Expect(err).NotTo(HaveOccurred())
		}
		insertFile("Meet - A", "feature-x", "a.go", true, day, 999999)
		insertFile("Meet - A", "feature-x", "a.go", true, day.Add(time.Minute), 120)
		insertFile("Meet - B", "feature-y", "b.go", false, day.Add(2*time.Minute), 999999)
		insertFile("Meet - B", "feature-y", "b.go", false, day.Add(3*time.Minute), 120)
		start, end := day.AddDate(0, 0, -1), day.AddDate(0, 0, 1)

		createRegexRenameG(d, ctx, sender, "project", "^Meet", "Meeting")
		rs := loadRenamesG(d, ctx, sender)

		ex, err := d.GetProjectExtras(ctx, sender, "Meeting", start, end, 15, "UTC", rs)
		Expect(err).NotTo(HaveOccurred())
		var write, read int64
		for _, e := range ex.Daily {
			write += e.WriteSeconds
			read += e.ReadSeconds
		}
		Expect(write).To(BeEquivalentTo(120))
		Expect(read).To(BeEquivalentTo(120))
		branchSet := map[string]int64{}
		for _, b := range ex.Branches {
			branchSet[b.Branch] += b.TotalSeconds
		}
		Expect(branchSet["feature-x"]).To(BeEquivalentTo(120))
		Expect(branchSet["feature-y"]).To(BeEquivalentTo(120))

		Expect(rawCountG(d, ctx, sender, "project", "Meet - A")).NotTo(Equal(0))
		Expect(rawCountG(d, ctx, sender, "project", "Meeting")).To(Equal(0))

		createRenameG(d, ctx, sender, "branch", "feature-x", "features")
		createRenameG(d, ctx, sender, "branch", "feature-y", "features")
		rs = loadRenamesG(d, ctx, sender)
		ex2, err := d.GetProjectExtras(ctx, sender, "Meeting", start, end, 15, "UTC", rs)
		Expect(err).NotTo(HaveOccurred())
		merged := map[string]int64{}
		for _, b := range ex2.Branches {
			merged[b.Branch] += b.TotalSeconds
		}
		_, ok := merged["feature-x"]
		Expect(ok).To(BeFalse())
		Expect(merged["features"]).To(BeEquivalentTo(240))
	})

	ginkgo.It("CheckProjectDisplayOwner: display name resolves through remap; non-matching/bogus does not", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("rendispown")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		cleanupSenderG(d, ctx, sender)
		ensureProjectsG(d, ctx, sender, "Meet - A", "Meet - B", "keep")

		empty := RenameSets{}
		ok, err := d.CheckProjectDisplayOwner(ctx, sender, "Meet - A", empty)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, err = d.CheckProjectDisplayOwner(ctx, sender, "Meeting", empty)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		createRegexRenameG(d, ctx, sender, "project", "^Meet", "Meeting")
		rs := loadRenamesG(d, ctx, sender)
		ok, err = d.CheckProjectDisplayOwner(ctx, sender, "Meeting", rs)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, err = d.CheckProjectDisplayOwner(ctx, sender, "Meet - A", rs)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		ok, err = d.CheckProjectDisplayOwner(ctx, sender, "keep", rs)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, _ = d.CheckProjectDisplayOwner(ctx, sender, "nope", rs)
		Expect(ok).To(BeFalse())
	})
})
