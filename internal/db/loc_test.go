// loc_test.go — ginkgo specs for the lines-of-code aggregations (gaka-yfg).
// Covers: the generated/vendored ignore filter DE-INFLATES the per-project sum
// (node_modules excluded), DISTINCT-ON picks each file's LATEST line count,
// a file shared across projects counts in both, the over-time curve grows
// monotonically to the current total, and the pure downsampler bounds +
// carry-forward-fills the point series.
package db

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// insertLocHB inserts one file heartbeat carrying file_lines (the harness's
// hbSeed has no file_lines column, so LOC tests insert directly).
func insertLocHB(d *DB, ctx context.Context, sender, project, entity string, lines int64, ts time.Time) {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO heartbeats
		  (sender, project, entity, ty, time_sent, user_agent, gap_seconds, file_lines)
		VALUES ($1,$2,$3,'file',$4,'ua',60,$5)`,
		sender, project, entity, ts, lines)
	Expect(err).NotTo(HaveOccurred())
}

func locBy(rows []model.LocProject) map[string]int64 {
	m := make(map[string]int64, len(rows))
	for _, r := range rows {
		m[r.Project] = r.Loc
	}
	return m
}

func pointOf(date string, loc int64) model.LocPoint {
	return model.LocPoint{Date: date, Loc: loc}
}

var _ = ginkgo.Describe("GetProjectLoc", func() {
	ginkgo.It("de-inflates by excluding generated/vendored files and sums each file's LATEST file_lines", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "loc")
		sender := f.Sender()
		f.Projects("alpha", "beta")

		base := time.Date(2025, 6, 2, 10, 0, 0, 0, time.UTC)
		// alpha: main.go grows 100 -> 120 (latest wins); util.go = 50.
		insertLocHB(d, ctx, sender, "alpha", "alpha/main.go", 100, base)
		insertLocHB(d, ctx, sender, "alpha", "alpha/main.go", 120, base.Add(time.Hour))
		insertLocHB(d, ctx, sender, "alpha", "alpha/util.go", 50, base)
		// alpha junk that MUST be filtered out (would add 90_000+ inflated lines).
		insertLocHB(d, ctx, sender, "alpha", "alpha/node_modules/react/index.js", 80000, base)
		insertLocHB(d, ctx, sender, "alpha", "alpha/dist/bundle.min.js", 9000, base)
		insertLocHB(d, ctx, sender, "alpha", "alpha/yarn.lock", 4000, base)
		insertLocHB(d, ctx, sender, "alpha", "alpha/api.pb.go", 2000, base)
		// beta: one real file.
		insertLocHB(d, ctx, sender, "beta", "beta/server.go", 300, base)

		t0 := base.AddDate(0, 0, -1)
		t1 := base.AddDate(0, 0, 1)

		rows, total, err := d.GetProjectLoc(ctx, sender, t0, t1, HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		by := locBy(rows)

		// alpha = 120 (latest main.go) + 50 (util.go); junk excluded.
		Expect(by["alpha"]).To(BeEquivalentTo(170), "node_modules/dist/lock/pb.go must be filtered out")
		Expect(by["beta"]).To(BeEquivalentTo(300))
		Expect(total).To(BeEquivalentTo(470))

		// Proof of de-inflation: WITHOUT the filter the raw sum would be enormous.
		var rawSum int64
		err = d.Pool.QueryRow(ctx, `
			SELECT CAST(sum(file_lines) AS int8) FROM (
			  SELECT DISTINCT ON (project, entity) file_lines
			  FROM heartbeats WHERE sender=$1 AND ty='file' AND file_lines IS NOT NULL
			  ORDER BY project, entity, time_sent DESC
			) x`, sender).Scan(&rawSum)
		Expect(err).NotTo(HaveOccurred())
		// Raw (unfiltered) sum: 95000 junk (node_modules 80k + dist 9k + lock 4k +
		// pb.go 2k) + 170 alpha real + 300 beta real = 95470.
		Expect(rawSum).To(BeEquivalentTo(95470), "unfiltered raw sum is inflated by generated files")
		Expect(total).To(BeNumerically("<", rawSum/100), "filter removes >99% inflation")
	})

	ginkgo.It("counts a file shared across two projects in BOTH (DISTINCT ON project,entity)", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "locshared")
		sender := f.Sender()
		f.Projects("web", "mobile")

		base := time.Date(2025, 6, 3, 10, 0, 0, 0, time.UTC)
		// Distinct time_sent per row — unique_heartbeats is (entity, sender,
		// time_sent), so the two projects' edits of the shared file need
		// different instants. DISTINCT ON (project, entity) still keeps them apart.
		insertLocHB(d, ctx, sender, "web", "shared/types.ts", 200, base)
		insertLocHB(d, ctx, sender, "mobile", "shared/types.ts", 200, base.Add(time.Minute))

		rows, total, err := d.GetProjectLoc(ctx, sender, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1), HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		by := locBy(rows)
		Expect(by["web"]).To(BeEquivalentTo(200))
		Expect(by["mobile"]).To(BeEquivalentTo(200))
		Expect(total).To(BeEquivalentTo(400))
	})

	ginkgo.It("excludes hidden projects", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "lochide")
		sender := f.Sender()
		f.Projects("keep", "secret")

		base := time.Date(2025, 6, 4, 10, 0, 0, 0, time.UTC)
		insertLocHB(d, ctx, sender, "keep", "keep/a.go", 100, base)
		insertLocHB(d, ctx, sender, "secret", "secret/b.go", 999, base)

		hs := mkHiddenSets(map[string][]string{"project": {"secret"}})
		rows, total, err := d.GetProjectLoc(ctx, sender, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1), hs, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		by := locBy(rows)
		Expect(by).NotTo(HaveKey("secret"))
		Expect(by["keep"]).To(BeEquivalentTo(100))
		Expect(total).To(BeEquivalentTo(100))
	})
})

var _ = ginkgo.Describe("GetLocOverTime", func() {
	ginkgo.It("grows to the current total and excludes generated files", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "locot")
		sender := f.Sender()
		f.Projects("alpha")

		// Day 1: a.go=100. Day 3: a.go grows to 140, b.go=60 appears. A junk
		// node_modules file with 50_000 lines on day 2 must never move the curve.
		d1 := time.Date(2025, 6, 10, 12, 0, 0, 0, time.UTC)
		insertLocHB(d, ctx, sender, "alpha", "alpha/a.go", 100, d1)
		insertLocHB(d, ctx, sender, "alpha", "alpha/node_modules/x/i.js", 50000, d1.AddDate(0, 0, 1))
		insertLocHB(d, ctx, sender, "alpha", "alpha/a.go", 140, d1.AddDate(0, 0, 2))
		insertLocHB(d, ctx, sender, "alpha", "alpha/b.go", 60, d1.AddDate(0, 0, 2))

		t0 := d1.AddDate(0, 0, -1)
		t1 := d1.AddDate(0, 0, 4)
		pts, err := d.GetLocOverTime(ctx, sender, t0, t1, HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(pts)).To(BeNumerically(">", 0))

		// Monotonic non-decreasing, and the final point == current total (200),
		// with the 50k junk file excluded.
		var prev int64 = -1
		for _, p := range pts {
			Expect(p.Loc).To(BeNumerically(">=", prev), "curve must not decrease")
			prev = p.Loc
		}
		Expect(pts[len(pts)-1].Loc).To(BeEquivalentTo(200))
		Expect(pts[len(pts)-1].Loc).To(BeNumerically("<", 50000))
	})

	ginkgo.It("carries a pre-range baseline into an in-range window", func() {
		d := openTestDBG()
		ctx := context.Background()
		f := newSenderG(d, "locbase")
		sender := f.Sender()
		f.Projects("alpha")

		// File established BEFORE the query window (500 lines), never edited again.
		pre := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
		insertLocHB(d, ctx, sender, "alpha", "alpha/legacy.go", 500, pre)

		// Query a window a month later: baseline must show up as 500.
		t0 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		t1 := time.Date(2025, 6, 5, 0, 0, 0, 0, time.UTC)
		pts, err := d.GetLocOverTime(ctx, sender, t0, t1, HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(pts)).To(BeNumerically(">", 0))
		Expect(pts[len(pts)-1].Loc).To(BeEquivalentTo(500), "pre-range baseline must be carried forward")
	})
})

// bucketLocDaily is pure — this spec needs no DB.
var _ = ginkgo.Describe("bucketLocDaily", func() {
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	ginkgo.It("returns empty for no input", func() {
		Expect(bucketLocDaily(nil, day(2025, 1, 1), day(2025, 1, 10))).To(BeEmpty())
	})

	ginkgo.It("keeps daily granularity on short ranges and carry-forward-fills gaps", func() {
		daily := []locDay{
			{day: day(2025, 1, 1), loc: 100},
			{day: day(2025, 1, 3), loc: 150}, // note: no point on the 2nd
		}
		pts := bucketLocDaily(daily, day(2025, 1, 1), day(2025, 1, 4))
		Expect(pts).To(HaveLen(4))
		Expect(pts[0]).To(Equal(pointOf("2025-01-01", 100)))
		Expect(pts[1]).To(Equal(pointOf("2025-01-02", 100)), "gap day carries prior value")
		Expect(pts[2]).To(Equal(pointOf("2025-01-03", 150)))
		Expect(pts[3]).To(Equal(pointOf("2025-01-04", 150)), "trailing day carries last value")
	})

	ginkgo.It("bounds the point count on multi-year ranges", func() {
		t0 := day(2020, 1, 1)
		t1 := day(2025, 1, 1) // ~1827 days
		daily := []locDay{{day: t0, loc: 10}, {day: t1, loc: 9999}}
		pts := bucketLocDaily(daily, t0, t1)
		Expect(len(pts)).To(BeNumerically("<=", locMaxOverTimePoints))
		Expect(len(pts)).To(BeNumerically(">", 0))
		Expect(pts[len(pts)-1].Loc).To(BeEquivalentTo(9999))
	})
})
