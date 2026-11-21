// rollup_path_test.go — behavioral pins for the SINGLE rollup-vs-raw
// selection path (rollup_path.go) and the single gap-cutoff source of truth.
//
// Non-tautological by construction: the behavioral specs drive the
// dispatcher with seeded heartbeats and assert on returned totals, so they
// fail under real mutations:
//
//   - drop the limit check from rollupEligible → the "non-default limit" spec
//     returns 0 (the rollup materialized the 20-min-gap beat as unattributed)
//     instead of the raw 30-min-limit total.
//   - change the cutoff in only ONE of its two bound homes (read-side minutes
//     param vs ingest-side defaultGapCutoffSeconds) → the boundary-parity spec
//     diverges.
//   - make entity Space arms actually filter (or stop routing them off the
//     rollup) → the entity-inertness spec fails, forcing a conscious update
//     of the documented semantics.
//   - remove a leg of rollupEligible → the truth-table spec fails.
//
// The suite only runs when the isolated test DB is available (openTestDBG
// skips otherwise).
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("rollup-vs-raw single selection path (rollup_path.go)", func() {
	day := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	start := day.AddDate(0, 0, -1)
	end := day.AddDate(0, 0, 2)

	// seedEntities writes two attributed chains distinguished ONLY by entity:
	// keep.go (600s) and drop.go (900s). Returns the sender.
	seedEntities := func(d *DB) string {
		f := newSenderG(d, "rollpath")
		f.Projects("alpha")
		for i, e := range []struct {
			entity string
			gap    int64
		}{{"keep.go", 600}, {"drop.go", 900}} {
			base := day.Add(time.Duration(i) * time.Hour)
			brk := hbSeed{project: "alpha", language: "Go", editor: "vim",
				platform: "linux", machine: "m", branch: "main",
				category: "Coding", entity: e.entity, ts: base, gap: 999999}
			insertSeedG(d, f.Ctx(), f.Sender(), brk)
			beat := brk
			beat.ts = base.Add(time.Minute)
			beat.gap = e.gap
			insertSeedG(d, f.Ctx(), f.Sender(), beat)
		}
		Expect(d.RefreshRollup(f.Ctx(), f.Sender(), start)).To(Succeed())
		return f.Sender()
	}

	ginkgo.It("routes an entity Space rule to the raw path, where it currently matches NOTHING (inert on both paths)", func() {
		// DISCOVERED while writing this suite: although HasMemberOutside
		// makes the gate route entity-rule spaces to the raw scan, the raw
		// path cannot apply the rule either — inclusionPredicate only arms
		// the 8 dashboard axes (hiddenAxes) and rawHeartbeatCols has no
		// entity column mapping, so an entity-only requested space collapses
		// to AND FALSE on BOTH paths. The fork is therefore output-equivalent
		// for every loadable member set; its only LIVE selector is the
		// timeLimit (pinned below). This spec pins the current semantics so
		// that making entity scopes actually filter (a feature decision)
		// must consciously update it.
		d := openTestDBG()
		sender := seedEntities(d)
		ctx := context.Background()
		ms := MemberSets{byAxis: map[string]axisMembers{
			"entity": {exact: []string{"keep.go"}},
		}}
		Expect(ms.HasMemberOutside(RollupAxes)).To(BeTrue(),
			"the gate DOES see the entity rule and routes to raw")
		Expect(rollupEligible(DefaultTimeLimitMinutes, HiddenSets{}, ms, true)).To(BeFalse())

		roll, err := d.GetUserActivityRollup(ctx, sender, start, end,
			HiddenSets{}, RenameSets{}, ms, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(roll)).To(BeZero(),
			"rollup: no entity column, scope matches nothing")

		rows, err := d.UserActivity(ctx, sender, start, end,
			DefaultTimeLimitMinutes, "UTC", HiddenSets{}, RenameSets{}, ms, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rows)).To(BeZero(),
			"raw (via dispatcher): entity arms are not built either — same empty result")
	})

	ginkgo.It("pins the gate truth table (rollupEligible is the only expression of the fork)", func() {
		entityMs := MemberSets{byAxis: map[string]axisMembers{
			"entity": {exact: []string{"keep.go"}},
		}}
		projectMs := MemberSets{byAxis: map[string]axisMembers{
			"project": {exact: []string{"alpha"}},
		}}
		Expect(rollupEligible(DefaultTimeLimitMinutes, HiddenSets{}, MemberSets{}, false)).To(BeTrue())
		Expect(rollupEligible(DefaultTimeLimitMinutes+1, HiddenSets{}, MemberSets{}, false)).To(BeFalse(),
			"non-default limit forces raw")
		Expect(rollupEligible(DefaultTimeLimitMinutes, HiddenSets{}, projectMs, true)).To(BeTrue(),
			"rollup-axis space rules stay on the fast path")
		Expect(rollupEligible(DefaultTimeLimitMinutes, HiddenSets{}, entityMs, true)).To(BeFalse(),
			"non-rollup-axis space rules force raw")
		Expect(rollupEligible(DefaultTimeLimitMinutes, HiddenSets{}, entityMs, false)).To(BeTrue(),
			"an unrequested space never forces raw")
		// The hidden leg: unreachable through LoadHiddenSets (pinned below),
		// but the gate must still honor a hypothetical non-rollup hide axis —
		// this keeps the leg mutation-detectable.
		entityHide := mkHiddenSets(map[string][]string{"entity": {"secret.go"}})
		Expect(rollupEligible(DefaultTimeLimitMinutes, entityHide, MemberSets{}, false)).To(BeFalse(),
			"a hide on a non-rollup axis forces raw")
	})

	ginkgo.It("serves the rollup when the same entity rule exists but NO space is requested", func() {
		d := openTestDBG()
		sender := seedEntities(d)
		ctx := context.Background()
		ms := MemberSets{byAxis: map[string]axisMembers{
			"entity": {exact: []string{"keep.go"}},
		}}
		// spaceRequested=false → the member set is inert; both chains count.
		rows, err := d.UserActivity(ctx, sender, start, end,
			DefaultTimeLimitMinutes, "UTC", HiddenSets{}, RenameSets{}, ms, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(rows)).To(Equal(int64(1500)))
	})

	ginkgo.It("routes a non-default limit to the raw scan (the rollup is materialized at the default cutoff)", func() {
		d := openTestDBG()
		f := newSenderG(d, "rolllim")
		f.Projects("alpha")
		ctx := context.Background()
		brk := hbSeed{project: "alpha", language: "Go", editor: "vim",
			platform: "linux", machine: "m", branch: "main",
			category: "Coding", entity: "a.go", ts: day, gap: 999999}
		insertSeedG(d, f.Ctx(), f.Sender(), brk)
		beat := brk
		beat.ts = day.Add(time.Minute)
		beat.gap = 20 * 60 // 20-min gap: counts at limit 30, zero at default
		insertSeedG(d, f.Ctx(), f.Sender(), beat)
		Expect(d.RefreshRollup(ctx, f.Sender(), start)).To(Succeed())

		at30, err := d.UserActivity(ctx, f.Sender(), start, end, 30, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(at30)).To(Equal(int64(20*60)),
			"limit=30 must hit the raw scan, which attributes the 20-min gap")

		atDefault, err := d.UserActivity(ctx, f.Sender(), start, end,
			DefaultTimeLimitMinutes, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(atDefault)).To(BeZero(),
			"at the default limit the 20-min gap is not attributed on either path")
	})

	ginkgo.It("keeps the materialized rollup cutoff and the raw minutes param in agreement at the boundary", func() {
		// Drift guard for the single cutoff constant: one beat AT the cutoff
		// (attributed) and one 1s OVER it (not). If the ingest-side bound
		// (defaultGapCutoffSeconds) and the read-side minutes param
		// (DefaultTimeLimitMinutes * 60 in SQL) ever disagree, raw and rollup
		// diverge here.
		d := openTestDBG()
		f := newSenderG(d, "rollcut")
		f.Projects("alpha")
		ctx := context.Background()
		tmpl := hbSeed{project: "alpha", language: "Go", editor: "vim",
			platform: "linux", machine: "m", branch: "main",
			category: "Coding", entity: "a.go", ts: day, gap: 999999}
		insertSeedG(d, f.Ctx(), f.Sender(), tmpl)
		at := tmpl
		at.ts = day.Add(time.Minute)
		at.gap = defaultGapCutoffSeconds
		insertSeedG(d, f.Ctx(), f.Sender(), at)
		over := tmpl
		over.ts = day.Add(2 * time.Minute)
		over.gap = defaultGapCutoffSeconds + 1
		insertSeedG(d, f.Ctx(), f.Sender(), over)
		Expect(d.RefreshRollup(ctx, f.Sender(), start)).To(Succeed())

		raw, err := d.GetUserActivity(ctx, f.Sender(), start, end,
			DefaultTimeLimitMinutes, "UTC", HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		roll, err := d.GetUserActivityRollup(ctx, f.Sender(), start, end,
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(totalStatSeconds(raw)).To(Equal(defaultGapCutoffSeconds),
			"exactly-at-cutoff attributes, one-over does not (raw)")
		Expect(totalStatSeconds(roll)).To(Equal(totalStatSeconds(raw)),
			"materialized cutoff must equal the raw read-side cutoff")
	})

	ginkgo.It("proves loadable hide rules can NEVER force the raw path (every hiddenAxis is a rollup axis)", func() {
		d := openTestDBG()
		f := newSenderG(d, "rollhide")
		ctx := context.Background()
		// A hide rule on EVERY dashboard axis, plus one on entity — which the
		// loader must EXCLUDE (axis = ANY(hiddenAxes) is the exact mechanism
		// that keeps the hidden-only fallback dead at the public-profile /
		// awards / widget-def sites).
		for _, axis := range hiddenAxes {
			_, err := d.CreateCurationRule(ctx, f.Sender(), axis, CurationHide,
				MatchExact, "secret-"+axis, nil)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := d.CreateCurationRule(ctx, f.Sender(), "entity", CurationHide,
			MatchExact, "secret-entity", nil)
		Expect(err).NotTo(HaveOccurred())

		hs, err := d.LoadHiddenSets(ctx, f.Sender())
		Expect(err).NotTo(HaveOccurred())
		Expect(hs.AnyHidden()).To(BeTrue())
		for _, axis := range hiddenAxes {
			Expect(hs.Values(axis)).To(ContainElement("secret-" + axis))
		}
		Expect(hs.Values("entity")).To(BeEmpty(),
			"LoadHiddenSets must not admit non-dashboard axes")
		Expect(hs.HasHiddenOutside(RollupAxes)).To(BeFalse(),
			"every loadable hide axis is a rollup axis — the hidden-only raw fallback is unreachable")
	})
})
