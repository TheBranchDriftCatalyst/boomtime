// curation_ginkgo_test.go — ginkgo mirror of curation_test.go (gaka-0vp.13).
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//   TestExclusionPredicateShape   → "exclusionPredicate: shape + arg indexing"
//   TestExclusionPredicateEmpty   → "exclusionPredicate: empty HiddenSets no-ops"
//   TestInjectAfterAnchorsExist   → "injectAfter: every anchor still exists in embedded SQL"
//   TestHideExclusionInStats      → "hide exclusion drops the hidden project from raw/rollup/project-list"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("curation exclusion", func() {
	ginkgo.It("exclusionPredicate produces the expected SQL fragment and arg indexing", func() {
		hs := mkHiddenSets(map[string][]string{
			"project": {"secret"},
			"machine": {"laptop", "desktop"},
		})
		args := []any{"sender", time.Now(), time.Now(), int64(15)}
		sql, outArgs, next := exclusionPredicate(hs, rawHeartbeatCols, "", 5, args)

		// hiddenAxes order puts project before machine, so $5=project, $6=machine.
		// lower() wraps the column so hides catch case-variant raw values (values
		// are pre-lowered by LoadHiddenSets, so ANY() is a plain lowercase equality).
		want := " AND NOT (lower(project) = ANY($5)) AND NOT (lower(machine) = ANY($6))"
		Expect(sql).To(Equal(want))
		Expect(next).To(Equal(7))
		// Two array args appended (project set, machine set); others empty → skipped.
		Expect(outArgs).To(HaveLen(6))
	})

	ginkgo.It("exclusionPredicate with empty HiddenSets is a no-op", func() {
		sql, args, next := exclusionPredicate(HiddenSets{}, rawHeartbeatCols, "", 5, []any{"x"})
		Expect(sql).To(Equal(""))
		Expect(next).To(Equal(5))
		Expect(args).To(HaveLen(1))
		Expect((HiddenSets{}).AnyHidden()).To(BeFalse())
	})

	ginkgo.It("every anchor for the hide-exclusion splice still exists in the embedded SQL", func() {
		anchors := []struct {
			name  string
			query string
			anch  string
		}{
			{"activity", qGetUserActivity, activityRangeAnchor},
			{"rollup", qGetUserActivityRoll, rollupRangeAnchor},
			{"projects_stats", qGetProjectsStats, projectStatsRangeAnchor},
			{"leaderboards", qGetLeaderboards, leaderboardsRangeAnchor},
			{"time_today", qGetTimeToday, timeTodayRangeAnchor},
			{"category_daily", qGetCategoryDaily, bigBetRangeAnchor},
			{"punchcard", qGetPunchcard, bigBetRangeAnchor},
			{"sessions", qGetSessions, bigBetRangeAnchor},
			{"momentum", qGetMomentum, bigBetRangeAnchor},
		}
		for _, a := range anchors {
			got := injectAfter(a.query, a.anch, "X")
			Expect(got).NotTo(Equal(a.query),
				"%s: anchor %q not found — exclusion would be a silent no-op", a.name, a.anch)
		}
		// Empty addition is a no-op.
		Expect(injectAfter(qGetUserActivity, activityRangeAnchor, "")).To(Equal(qGetUserActivity))
	})

	ginkgo.It("hide exclusion drops the hidden project from raw, rollup, and project-list paths", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := "curate3_" + time.Now().Format("150405.000000")
		_, err := d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		Expect(err).NotTo(HaveOccurred())
		_, err = d.Pool.Exec(ctx, `INSERT INTO projects (owner,name) VALUES ($1,'keep'),($1,'hideme') ON CONFLICT DO NOTHING`, sender)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE sender=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM hb_rollup_daily WHERE sender=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
		})

		base := time.Date(2025, 5, 3, 10, 0, 0, 0, time.UTC)
		for i := 0; i < 3; i++ {
			seedHBG(d, ctx, sender, "keep", "Go", base.Add(time.Duration(i)*time.Minute))
		}
		for i := 0; i < 2; i++ {
			seedHBG(d, ctx, sender, "hideme", "Go", base.Add(time.Duration(10+i)*time.Minute))
		}
		Expect(d.RecomputeGaps(ctx, sender, base.AddDate(0, 0, -1))).To(Succeed())
		Expect(d.RefreshRollup(ctx, sender, base.AddDate(0, 0, -1))).To(Succeed())

		start := base.AddDate(0, 0, -1)
		end := base.AddDate(0, 0, 1)

		// No hide: both projects appear on the raw path.
		rows, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasProject(rows, "hideme")).To(BeTrue(), "expected 'hideme' project before hiding")

		// Add a hide rule and reload the hidden sets.
		_, err = d.CreateCurationRule(ctx, sender, "project", "hide", "exact", "hideme", nil)
		Expect(err).NotTo(HaveOccurred())
		hs, err := d.LoadHiddenSets(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(hs.AnyHidden()).To(BeTrue(), "expected AnyHidden after adding a project hide")

		// Raw path excludes it.
		rows, err = d.GetUserActivity(ctx, sender, start, end, 15, "UTC", hs, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasProject(rows, "hideme")).To(BeFalse(), "raw activity should exclude the hidden project")
		Expect(hasProject(rows, "keep")).To(BeTrue(), "raw activity should still include the kept project")

		// Rollup fast path excludes it too.
		rrows, err := d.GetUserActivityRollup(ctx, sender, start, end, hs, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasProject(rrows, "hideme")).To(BeFalse(), "rollup should exclude the hidden project")
		Expect(hasProject(rrows, "keep")).To(BeTrue(), "rollup should still include the kept project")

		// Projects list excludes it.
		projects, err := d.GetAllProjects(ctx, sender, start, end, hs, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(projects).NotTo(ContainElement("hideme"), "project list should exclude the hidden project")
	})
})
