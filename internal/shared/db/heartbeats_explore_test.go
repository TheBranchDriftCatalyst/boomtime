// heartbeats_explore_ginkgo_test.go — ginkgo mirror of heartbeats_explore_test.go (boom-0vp.13).
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//
//	TestExploreColumnWhitelist    → "ExploreColumn > whitelist + rejects"
//	TestBuildFilterClause         → "buildFilterClause > shape"
//	TestGroupHeartbeatsDayShape   → "GroupHeartbeats + ListHeartbeats > shape"
//	TestLatestHeartbeat           → "LatestHeartbeat > empty + populated"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("heartbeats explorer", func() {
	ginkgo.It("ExploreColumn whitelist maps documented axes and rejects everything else", func() {
		want := map[string]string{
			"day":       "time_sent::date",
			"project":   "project",
			"language":  "language",
			"editor":    "editor",
			"plugin":    "plugin",
			"platform":  "platform",
			"machine":   "machine",
			"branch":    "branch",
			"category":  "category",
			"type":      "ty",
			"entity":    "entity",
			"isWrite":   "is_write",
			"userAgent": "user_agent",
		}
		for name, col := range want {
			got, ok := ExploreColumn(name)
			Expect(ok).To(BeTrue(), "ExploreColumn(%q) should be whitelisted", name)
			Expect(got).To(Equal(col))
		}
		for _, bad := range []string{"sender", "id", "ty", "time_sent", "is_write", "user_agent", "1; DROP TABLE heartbeats", ""} {
			_, ok := ExploreColumn(bad)
			Expect(ok).To(BeFalse(), "ExploreColumn(%q) should be rejected", bad)
		}
	})

	ginkgo.It("buildFilterClause emits case-folded eq and IS NULL branches with correct arg indexing", func() {
		col, _ := ExploreColumn("language")
		nullCol, _ := ExploreColumn("project")
		v := "Go"
		filters := []ExploreFilter{
			{Column: col, Value: &v},
			{Column: nullCol, Value: nil},
		}
		sql, args, next := buildFilterClause(filters, 4, []any{"sender", time.Now(), time.Now()})
		Expect(sql).To(Equal(" AND lower(language::text) = lower($4) AND project IS NULL"))
		Expect(next).To(Equal(5))
		Expect(args).To(HaveLen(4))
		Expect(args[3]).To(Equal("Go"))
	})

	ginkgo.It("GroupHeartbeats + ListHeartbeats produce the expected shape (day/lang groups, filter, entity substring)", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := "explore_user_" + time.Now().Format("150405.000000")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'proj') ON CONFLICT DO NOTHING`, sender)
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
		})

		base := time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC)
		insert := func(ts time.Time, lang *string, gap int) {
			_, err := d.Pool.Exec(ctx, `INSERT INTO heartbeats (sender, project, language, entity, ty, time_sent, user_agent, gap_seconds) VALUES ($1,'proj',$2,'a.go','file',$3,'ua',$4)`, sender, lang, ts, gap)
			Expect(err).NotTo(HaveOccurred())
		}
		goLang := "Go"
		insert(base, &goLang, 0)                   // first of the day: no prior gap
		insert(base.Add(time.Minute), &goLang, 60) // +60s attributed
		insert(base.AddDate(0, 0, 1), &goLang, 0)  // next day, first beat
		insert(base.Add(2*time.Minute), nil, 5000) // 5000s > 15*60 -> NOT attributed

		start := base.AddDate(0, 0, -1)
		end := base.AddDate(0, 0, 2)

		dayCol, _ := ExploreColumn("day")
		groups, trunc, err := d.GroupHeartbeats(ctx, sender, dayCol, start, end, nil, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		Expect(trunc).To(BeFalse())
		Expect(groups).To(HaveLen(2))
		Expect(groups[0].Value).NotTo(BeNil())
		Expect(*groups[0].Value).To(Equal("2025-04-01"))
		Expect(groups[0].Count).To(BeEquivalentTo(3))
		Expect(groups[0].Seconds).To(BeEquivalentTo(60), "top day: gaps within cutoff")

		langCol, _ := ExploreColumn("language")
		lgroups, _, err := d.GroupHeartbeats(ctx, sender, langCol, start, end, nil, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		var haveNull, haveGo bool
		for _, g := range lgroups {
			if g.Value == nil {
				haveNull = true
			} else if *g.Value == "Go" {
				haveGo = true
			}
		}
		Expect(haveGo).To(BeTrue())
		Expect(haveNull).To(BeTrue())

		filters := []ExploreFilter{{Column: langCol, Value: &goLang}}
		fg, _, err := d.GroupHeartbeats(ctx, sender, dayCol, start, end, filters, "", 500, 15)
		Expect(err).NotTo(HaveOccurred())
		var total int64
		for _, g := range fg {
			total += g.Count
		}
		Expect(total).To(BeEquivalentTo(3))

		items, cnt, err := d.ListHeartbeats(ctx, sender, start, end, filters, "a.g", 1, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(cnt).To(BeEquivalentTo(3))
		Expect(items).To(HaveLen(3))
		Expect(items[0].Type).To(Equal("file"))
		Expect(items[0].Entity).To(Equal("a.go"))
	})

	ginkgo.It("LatestHeartbeat returns (nil,0) for empty user and (max_ts, count) once populated", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := "latest_user_" + time.Now().Format("150405.000000")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		_, _ = d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'proj') ON CONFLICT DO NOTHING`, sender)
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
		})

		last, count, err := d.LatestHeartbeat(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(last).To(BeNil())
		Expect(count).To(BeEquivalentTo(0))

		t1 := time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC)
		t2 := time.Date(2025, 4, 3, 12, 30, 0, 0, time.UTC)
		for _, ts := range []time.Time{t1, t2, t1.Add(time.Hour)} {
			_, err := d.Pool.Exec(ctx, `INSERT INTO heartbeats (sender, project, entity, ty, time_sent, user_agent) VALUES ($1,'proj','a.go','file',$2,'ua')`, sender, ts)
			Expect(err).NotTo(HaveOccurred())
		}

		last, count, err = d.LatestHeartbeat(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(BeEquivalentTo(3))
		Expect(last).NotTo(BeNil())
		Expect(last.Equal(t2)).To(BeTrue())
		Expect(last.Location()).To(Equal(time.UTC))
	})
})
