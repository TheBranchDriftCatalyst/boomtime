// suppression_ginkgo_test.go — ginkgo mirror of suppression_test.go (gaka-0vp.13).
// 1:1 case map (1 stdlib TestXxx wrapping 8 subtests → 1 DescribeTable(8)):
//
//	TestSuppressedValuesExcludedFromAggregations → DescribeTable "hidden value excluded from every aggregation path" per-axis
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("suppressed values", func() {
	ginkgo.DescribeTable("are excluded from every aggregation path while remaining in audit surfaces",
		func(axis string) {
			d := openTestDBG()
			ctx := context.Background()

			const keepSecs = 300
			const supSecs = 120

			base := func(axis, val string, ts time.Time, gap int64) hbSeed {
				h := hbSeed{
					project: "keepProj", language: "Go", editor: "vim", plugin: "vim-wakatime",
					machine: "laptop", platform: "linux", branch: "main", category: "Coding",
					ts: ts, gap: gap,
				}
				switch axis {
				case "project":
					h.project = val
				case "language":
					h.language = val
				case "editor":
					h.editor = val
				case "plugin":
					h.plugin = val
				case "machine":
					h.machine = val
				case "platform":
					h.platform = val
				case "branch":
					h.branch = val
				case "category":
					h.category = val
				}
				return h
			}

			sender := mkSender("supp_" + axis)
			_, err := d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
			Expect(err).NotTo(HaveOccurred())
			cleanupSenderG(d, ctx, sender)

			keepVal, supVal := "KEEP", "SUPPRESS"
			if axis == "project" {
				ensureProjectsG(d, ctx, sender, keepVal, supVal)
			} else {
				ensureProjectsG(d, ctx, sender, "keepProj")
			}

			day := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
			insertSeedG(d, ctx, sender, base(axis, keepVal, day, 999999))
			insertSeedG(d, ctx, sender, base(axis, keepVal, day.Add(1*time.Minute), keepSecs))
			insertSeedG(d, ctx, sender, base(axis, supVal, day.Add(2*time.Minute), 999999))
			insertSeedG(d, ctx, sender, base(axis, supVal, day.Add(3*time.Minute), supSecs))

			Expect(d.RefreshRollup(ctx, sender, day.AddDate(0, 0, -1))).To(Succeed())

			start := day.AddDate(0, 0, -1)
			end := day.AddDate(0, 0, 1)

			// Baseline.
			noHide := HiddenSets{}
			rawBefore, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", noHide, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			if statRowHasAxis(axis) {
				Expect(statRowsContain(rawBefore, axis, supVal)).To(BeTrue(), "baseline should contain SUPPRESS on %s", axis)
				Expect(statRowsContain(rawBefore, axis, keepVal)).To(BeTrue(), "baseline should contain KEEP on %s", axis)
			}
			Expect(totalStatSeconds(rawBefore)).To(BeEquivalentTo(keepSecs + supSecs))

			// Add hide.
			_, err = d.CreateCurationRule(ctx, sender, axis, "hide", "exact", supVal, nil)
			Expect(err).NotTo(HaveOccurred())
			hs, err := d.LoadHiddenSets(ctx, sender)
			Expect(err).NotTo(HaveOccurred())

			// 1. Raw activity.
			raw, err := d.GetUserActivity(ctx, sender, start, end, 15, "UTC", hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			if statRowHasAxis(axis) {
				Expect(statRowsContain(raw, axis, supVal)).To(BeFalse(), "[raw] SUPPRESS still present on %s", axis)
				Expect(statRowsContain(raw, axis, keepVal)).To(BeTrue(), "[raw] KEEP was collaterally removed on %s", axis)
			}
			Expect(totalStatSeconds(raw)).To(BeEquivalentTo(keepSecs))

			// 2. Rollup fast path.
			if RollupAxes[axis] {
				roll, err := d.GetUserActivityRollup(ctx, sender, start, end, hs, RenameSets{}, MemberSets{}, false)
				Expect(err).NotTo(HaveOccurred())
				Expect(statRowsContain(roll, axis, supVal)).To(BeFalse(), "[rollup] SUPPRESS still present on %s", axis)
				Expect(totalStatSeconds(roll)).To(BeEquivalentTo(keepSecs))
				Expect(hs.HasHiddenOutside(RollupAxes)).To(BeFalse(), "[rollup] %s is a rollup axis", axis)
			} else {
				Expect(hs.HasHiddenOutside(RollupAxes)).To(BeTrue(), "[rollup] axis %s not in rollup; HasHiddenOutside must be true (raw fallback)", axis)
			}

			// 3. Category big-bet scan.
			categories, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC", hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			if axis == "category" {
				for _, c := range categories {
					Expect(c.Category).NotTo(Equal(supVal), "[category] SUPPRESS still in category rows")
				}
			}
			var catTotal int64
			for _, c := range categories {
				catTotal += c.TotalSeconds
			}
			Expect(catTotal).To(BeEquivalentTo(keepSecs))

			// 4. Big-bet endpoints.
			punch, err := d.GetPunchcard(ctx, sender, start, end, 15, "UTC", hs, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(sumPunch(punch)).To(BeEquivalentTo(keepSecs))
			sess, err := d.GetSessions(ctx, sender, start, end, 15, "UTC", hs, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(sumSessions(sess)).To(BeEquivalentTo(keepSecs))
			mom, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC", hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(sumMomentum(mom)).To(BeEquivalentTo(keepSecs))
			if axis == "project" {
				for _, m := range mom {
					Expect(m.Project).NotTo(Equal(supVal), "[momentum] hidden project still a row")
				}
			}

			// 5. Project list.
			if axis == "project" {
				projects, err := d.GetAllProjects(ctx, sender, start, end, hs, RenameSets{}, MemberSets{}, false)
				Expect(err).NotTo(HaveOccurred())
				Expect(projects).NotTo(ContainElement(supVal), "[projectList] hidden project still listed")
			}

			// 6. Leaderboards.
			lb, err := d.GetLeaderboards(ctx, start, end, sender, hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			for _, r := range lb {
				if r.Sender != sender {
					continue
				}
				Expect(lbRowAxis(r, axis)).NotTo(Equal(supVal), "[leaderboards] SUPPRESS present for requester on %s", axis)
			}
			var lbKeep int64
			for _, r := range lb {
				if r.Sender == sender {
					lbKeep += r.TotalSeconds
				}
			}
			Expect(lbKeep).To(BeEquivalentTo(keepSecs))

			// 7. Statusbar today.
			today, err := d.GetTotalTimeToday(ctx, sender, "UTC", hs)
			Expect(err).NotTo(HaveOccurred())
			Expect(today).To(BeEquivalentTo(0), "expected 0 for out-of-today seed")

			// Audit surfaces still contain SUPPRESS.
			col, _ := ExploreColumn(axis)
			groups, _, err := d.GroupHeartbeats(ctx, sender, col, start, end, nil, "", 500, 15)
			Expect(err).NotTo(HaveOccurred())
			Expect(groupsContain(groups, supVal)).To(BeTrue(), "[audit group] SUPPRESS must still appear on %s", axis)
			Expect(groupsContain(groups, keepVal)).To(BeTrue(), "[audit group] KEEP missing on %s", axis)

			items, _, err := d.ListHeartbeats(ctx, sender, start, end, nil, "", 1, 500)
			Expect(err).NotTo(HaveOccurred())
			Expect(listContains(items, axis, supVal)).To(BeTrue(), "[audit list] SUPPRESS must still appear on %s", axis)

			last, count, err := d.LatestHeartbeat(ctx, sender)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(BeEquivalentTo(4))
			Expect(last).NotTo(BeNil())

			_, err = d.GetTimeline(ctx, sender, start, end, 15, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
		},
		ginkgo.Entry("project", "project"),
		ginkgo.Entry("language", "language"),
		ginkgo.Entry("editor", "editor"),
		ginkgo.Entry("plugin", "plugin"),
		ginkgo.Entry("machine", "machine"),
		ginkgo.Entry("platform", "platform"),
		ginkgo.Entry("branch", "branch"),
		ginkgo.Entry("category", "category"),
	)
})
