// eval_rename_integration_test.go — boom-l827 (audit 2026-09-06) regression:
// the goals evaluator matched RAW hb_rollup_daily values and ignored the
// owner's query-time rename rules, so a goal authored against the only name
// the FE ever shows for a renamed/merged project or language reported 0%
// forever.
//
// Named invariants pinned here:
//
//	"renamed project" — rule "myrepo-v2" → "MyRepo"; the rollup stores
//	"myrepo-v2"; a goal on "MyRepo" must count those seconds. Without the
//	expansion current == 0 and the goal can never be hit.
//
//	"merge of several sources" — two raw values renamed onto one display
//	name sum together (that is what a merge rule MEANS on a dashboard).
//
//	"no over-match" — a raw value that is NOT renamed onto the target stays
//	out of the total, so the expansion cannot inflate a goal.
//
//	"disabled rule" — a paused rename rule (boom-dfd) stops remapping the
//	dashboards, so it must stop expanding goals too.
//
//	"streak leaf" — the per-day evaluators evalStreak spawns inherit the
//	expansion (they are separate evaluator values sharing the rename cache).
package goals_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// renameRuleG creates an enabled, query-time (NOT apply-at-ingest) exact
// rename rule through the same DB entry point the curation handler uses.
func renameRuleG(hz *testutil.Harness, owner, axis, from, to string) {
	GinkgoHelper()
	_, err := hz.DB.CreateCurationRuleWithIngest(context.Background(), owner,
		axis, db.CurationRename, db.MatchExact, from, &to, false)
	Expect(err).NotTo(HaveOccurred(), "create rename rule %s: %s -> %s", axis, from, to)
}

var _ = Describe("Evaluate + rename rules (boom-l827)", func() {
	It("counts a renamed project's RAW rows for a goal authored on the display name", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_rename_proj")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		// Raw heartbeats/rollup carry the SOURCE name — a rename is query-time
		// only, it never rewrites stored rows.
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "myrepo-v2", "Go", "vim", 4000)
		renameRuleG(hz, owner, "project", "myrepo-v2", "MyRepo")

		// The goal is authored against "MyRepo" — the ONLY name the projects
		// list, dashboards and widget links show once the rule exists.
		spec := `{"kind":"time","axis":"project","value":"MyRepo","op":">=","target_seconds":3600,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())

		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions).To(HaveLen(1))
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(4000),
			"goal on the renamed (displayed) project name reported %d seconds — the evaluator is still matching raw rollup values and ignoring the owner's rename rules",
			prog.SubConditions[0].Current)
		Expect(prog.Hit).To(BeTrue(), "4000s against a 3600s target must hit")
		Expect(prog.Progress).To(Equal(float64(1)))
	})

	It("sums every source of a MERGE rename (two raw names → one display name)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_rename_merge")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "P", "Golang", "vim", 1000)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -2), "P", "go-lang", "vim", 1500)
		// A language row that is NOT part of the merge — must stay out.
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -2), "P", "Rust", "vim", 7000)
		renameRuleG(hz, owner, "language", "Golang", "Go")
		renameRuleG(hz, owner, "language", "go-lang", "Go")

		spec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":2000,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())

		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(2500),
			"merged goal should sum both renamed sources (1000+1500) and exclude the unrelated Rust row")
	})

	It("ignores a DISABLED rename rule (paused rules stop remapping — boom-dfd)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_rename_off")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, owner, now.AddDate(0, 0, -1), "raw-proj", "Go", "vim", 5000)
		renameRuleG(hz, owner, "project", "raw-proj", "Pretty")

		rules, err := hz.DB.ListCurationRules(context.Background(), owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(rules).NotTo(BeEmpty())
		_, err = hz.DB.SetCurationRuleEnabled(context.Background(), owner, rules[0].ID, false)
		Expect(err).NotTo(HaveOccurred())

		spec := `{"kind":"time","axis":"project","value":"Pretty","op":">=","target_seconds":1,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())

		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(0),
			"a paused rename rule must not expand a goal — the dashboards stopped merging, the goal must too")
	})

	It("does not leak another owner's rename rules into this owner's goal", func() {
		hz := testutil.NewHarness(GinkgoTB())
		ownerA, _ := hz.MintUser("eval_rename_a")
		ownerB, _ := hz.MintUser("eval_rename_b")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		seedRollupRowG(hz, ownerA, now.AddDate(0, 0, -1), "shared-raw", "Go", "vim", 3000)
		seedRollupRowG(hz, ownerB, now.AddDate(0, 0, -1), "shared-raw", "Go", "vim", 3000)
		// Only A has the rule.
		renameRuleG(hz, ownerA, "project", "shared-raw", "Display")

		spec := `{"kind":"time","axis":"project","value":"Display","op":">=","target_seconds":1,"window":"week"}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())

		progA, err := goals.Evaluate(context.Background(), hz.DB.Pool, ownerA, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progA.SubConditions[0].Current).To(BeEquivalentTo(3000))

		progB, err := goals.Evaluate(context.Background(), hz.DB.Pool, ownerB, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(progB.SubConditions[0].Current).To(BeEquivalentTo(0),
			"owner B has no rename rule; A's rule must not expand B's goal")
	})

	It("applies the expansion inside a streak's per-day leaf evaluations", func() {
		hz := testutil.NewHarness(GinkgoTB())
		owner, _ := hz.MintUser("eval_rename_streak")

		now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
		for d := 0; d < 3; d++ {
			seedRollupRowG(hz, owner, now.AddDate(0, 0, -d), "src-proj", "Go", "vim", 1200)
		}
		renameRuleG(hz, owner, "project", "src-proj", "Shown")

		spec := `{"kind":"streak","min_days":3,"condition":{"kind":"time","axis":"project","value":"Shown","op":">=","target_seconds":600,"window":"day"}}`
		p, err := goals.ValidateSpec(json.RawMessage(spec))
		Expect(err).NotTo(HaveOccurred())

		prog, err := goals.Evaluate(context.Background(), hz.DB.Pool, owner, p, now)
		Expect(err).NotTo(HaveOccurred())
		Expect(prog.SubConditions[0].Current).To(BeEquivalentTo(3),
			"per-day streak leaves must expand the rename too; got %d consecutive days",
			prog.SubConditions[0].Current)
		Expect(prog.Hit).To(BeTrue())
	})
})
