// rollup_parity_test.go (gaka-o4m): parity guarantees for the rollup fast-path
// variants of Momentum, Leaderboards, and CategoryDaily. Every parity test
// seeds the SAME heartbeats + refreshes the rollup, then asserts:
//
//	raw   == rollup   (default range)
//	raw   == rollup   (space-scoped inclusion — rollup-eligible axis)
//	raw   == rollup   (hidden values applied — rollup-eligible axis)
//
// Non-tautological: each rollup variant is written from a DIFFERENT source
// table (hb_rollup_daily vs heartbeats) so parity failing here catches a real
// SQL divergence — a coalesce miss, an unwrapped `_missing` filter, a
// forgotten predicate splice, or a `date_trunc` boundary drift. The tests
// only run when the isolated test DB is available (openTestDBG skips otherwise).
package db

import (
	"context"
	"sort"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("rollup fast-path parity (gaka-o4m)", func() {
	// One-day mid-UTC block seeded across three projects + two languages +
	// two categories so every rollup-eligible axis has at least two values
	// (a hide/scope predicate that filters one leaves the other in place —
	// so the diff-vs-raw is non-trivial).
	day := time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 2)

	// seedMixed writes 2 attributed heartbeats (gap=60s each) after a break
	// for each of the three project/language/category permutations. Refreshes
	// the rollup afterward so both raw and rollup paths see the same input.
	seedMixed := func(d *DB) string {
		f := newSenderG(d, "roll_par")
		f.Projects("alpha", "bravo", "charlie")
		type row struct{ proj, lang, cat string }
		rows := []row{
			{"alpha", "Go", "Coding"},
			{"bravo", "Go", "Coding"},
			{"bravo", "Rust", "Coding"},
			{"charlie", "Rust", "Meeting"},
		}
		for i, r := range rows {
			base := day.Add(time.Duration(i) * 30 * time.Minute)
			tmpl := hbSeed{
				project: r.proj, language: r.lang, editor: "vim", plugin: "pl",
				machine: "m", platform: "linux", branch: "main", category: r.cat,
				entity: "a.go",
			}
			// break sample so gap_seconds is unattributed on the first row
			brk := tmpl
			brk.ts = base
			brk.gap = 999999
			insertSeedG(d, f.Ctx(), f.Sender(), brk)
			for j := 0; j < 2; j++ {
				h := tmpl
				h.ts = base.Add(time.Duration(j+1) * time.Minute)
				h.gap = 60
				insertSeedG(d, f.Ctx(), f.Sender(), h)
			}
		}
		Expect(d.RefreshRollup(f.Ctx(), f.Sender(), start)).To(Succeed())
		return f.Sender()
	}

	// --- Momentum parity ------------------------------------------------

	ginkgo.Context("GetMomentumRollup vs GetMomentum", func() {
		ginkgo.It("matches at the default range (no hide, no scope)", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			raw, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC",
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetMomentumRollup(ctx, sender, start, end,
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(roll).To(Equal(raw), "rollup momentum must byte-match raw at default 15-min limit")
		})

		ginkgo.It("matches with a project-hide (rollup-eligible axis)", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			hs := mkHiddenSets(map[string][]string{"project": {"bravo"}})
			raw, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC",
				hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetMomentumRollup(ctx, sender, start, end,
				hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(roll).To(Equal(raw), "rollup must match raw with project hide")
			// non-tautological: the hidden project MUST be gone from BOTH sides.
			for _, m := range roll {
				Expect(m.Project).NotTo(Equal("bravo"))
			}
		})

		ginkgo.It("matches with a Space rule on a rollup axis (language=Go)", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			ms := MemberSets{byAxis: map[string]axisMembers{
				"language": {exact: []string{"go"}},
			}}
			raw, err := d.GetMomentum(ctx, sender, start, end, 15, "UTC",
				HiddenSets{}, RenameSets{}, ms, true)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetMomentumRollup(ctx, sender, start, end,
				HiddenSets{}, RenameSets{}, ms, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(roll).To(Equal(raw), "rollup must match raw under a Space scope on a rollup axis")
			// non-tautological: Space scope should have excluded charlie/Rust-only.
			names := map[string]struct{}{}
			for _, m := range roll {
				names[m.Project] = struct{}{}
			}
			Expect(names).NotTo(HaveKey("charlie"), "charlie is Rust-only; a language=Go scope must exclude it")
		})
	})

	// --- Leaderboards parity ---------------------------------------------

	ginkgo.Context("GetLeaderboardsRollup vs GetLeaderboards", func() {
		// stableLB sorts leaderboard rows to a deterministic order for slice
		// comparison (raw ORDER BY language leaves project/sender ties as
		// backend-dependent scan order, which the Go caller doesn't rely on).
		stableLB := func(rows []LeaderboardRow) []LeaderboardRow {
			out := make([]LeaderboardRow, len(rows))
			copy(out, rows)
			sort.SliceStable(out, func(i, j int) bool {
				if out[i].Sender != out[j].Sender {
					return out[i].Sender < out[j].Sender
				}
				if out[i].Language != out[j].Language {
					return out[i].Language < out[j].Language
				}
				return out[i].Project < out[j].Project
			})
			return out
		}

		ginkgo.It("matches at the default range (no hide, no scope)", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			raw, err := d.GetLeaderboards(ctx, start, end, sender,
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetLeaderboardsRollup(ctx, start, end, sender,
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(stableLB(roll)).To(Equal(stableLB(raw)),
				"rollup leaderboards must byte-match raw (query hardcodes 15-min gap)")
		})

		ginkgo.It("matches with a requester-scoped project hide", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			hs := mkHiddenSets(map[string][]string{"project": {"alpha"}})
			raw, err := d.GetLeaderboards(ctx, start, end, sender,
				hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetLeaderboardsRollup(ctx, start, end, sender,
				hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(stableLB(roll)).To(Equal(stableLB(raw)),
				"rollup + hide must match raw + hide")
			for _, r := range roll {
				if r.Sender == sender {
					Expect(r.Project).NotTo(Equal("alpha"),
						"the requester's own alpha row must be excluded")
				}
			}
		})

		ginkgo.It("matches with a Space scope on a rollup axis", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			ms := MemberSets{byAxis: map[string]axisMembers{
				"category": {exact: []string{"coding"}},
			}}
			raw, err := d.GetLeaderboards(ctx, start, end, sender,
				HiddenSets{}, RenameSets{}, ms, true)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetLeaderboardsRollup(ctx, start, end, sender,
				HiddenSets{}, RenameSets{}, ms, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(stableLB(roll)).To(Equal(stableLB(raw)),
				"rollup + space-scope must match raw + space-scope")
		})
	})

	// --- CategoryDaily parity --------------------------------------------

	ginkgo.Context("GetCategoryDailyRollup vs GetCategoryDaily", func() {
		// stableCat sorts (day, category) — raw orders by day only so
		// same-day category order is scan-dependent.
		stableCat := func(rows []CategoryDailyRow) []CategoryDailyRow {
			out := make([]CategoryDailyRow, len(rows))
			copy(out, rows)
			sort.SliceStable(out, func(i, j int) bool {
				if !out[i].Day.Equal(out[j].Day) {
					return out[i].Day.Before(out[j].Day)
				}
				return out[i].Category < out[j].Category
			})
			return out
		}

		ginkgo.It("matches at the default range (no hide, no scope)", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			raw, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC",
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetCategoryDailyRollup(ctx, sender, start, end,
				HiddenSets{}, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(stableCat(roll)).To(Equal(stableCat(raw)),
				"rollup category-daily must byte-match raw at the default 15-min limit")
		})

		ginkgo.It("matches with a project-hide (excludes 'bravo' from both)", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			hs := mkHiddenSets(map[string][]string{"project": {"bravo"}})
			raw, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC",
				hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetCategoryDailyRollup(ctx, sender, start, end,
				hs, RenameSets{}, MemberSets{}, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(stableCat(roll)).To(Equal(stableCat(raw)),
				"rollup + project-hide must match raw + project-hide")
		})

		ginkgo.It("matches with a Space scope on category=Coding", func() {
			d := openTestDBG()
			sender := seedMixed(d)
			ctx := context.Background()
			ms := MemberSets{byAxis: map[string]axisMembers{
				"category": {exact: []string{"coding"}},
			}}
			raw, err := d.GetCategoryDaily(ctx, sender, start, end, 15, "UTC",
				HiddenSets{}, RenameSets{}, ms, true)
			Expect(err).NotTo(HaveOccurred())
			roll, err := d.GetCategoryDailyRollup(ctx, sender, start, end,
				HiddenSets{}, RenameSets{}, ms, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(stableCat(roll)).To(Equal(stableCat(raw)),
				"rollup + space-scope must match raw + space-scope")
			// non-tautological: Meeting must be gone from both sides.
			for _, r := range roll {
				Expect(r.Category).NotTo(Equal("Meeting"))
			}
		})
	})
})
