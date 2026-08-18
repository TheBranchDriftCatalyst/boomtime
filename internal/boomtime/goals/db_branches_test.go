// db_branches_test.go — byte-identical branch-padding + input-validation
// Its lifted out of internal/db/branch_padding_test.go and
// internal/db/error_branches_test.go when the goals domain moved to
// internal/goals/ (gaka-8tn phase 2b).
//
// The Describe/It strings, Expect assertions, table data, and seeded
// values are preserved verbatim so grep against the original coverage
// baseline still succeeds. Only imports and call-site expressions
// changed (d.CreateGoal(...) → goals.CreateGoal(d, ...) etc.).
package goals_test

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/goals"
)

var _ = ginkgo.Describe("branch coverage padding (gaka-d6x)", func() {

	// ---- goals.itoaFast: zero + negative + multi-digit ----

	ginkgo.It("itoaFast: pins 0, negative, and multi-digit — matches strconv.Itoa on every non-huge int", func() {
		for _, n := range []int{0, 1, 9, 10, 99, 100, -1, -42, -12345} {
			Expect(goals.ItoaFastForTest(n)).To(Equal(strconv.Itoa(n)), "itoaFast(%d)", n)
		}
	})

	// ---- goals.ListGoals rejects empty owner ----

	ginkgo.It("ListGoals: empty owner rejects with a clear error", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, err := goals.ListGoals(d, ctx, "")
		Expect(err).To(HaveOccurred())
	})

	ginkgo.It("GetGoal: empty owner/id rejects with a clear error", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, err := goals.GetGoal(d, ctx, "", "id")
		Expect(err).To(HaveOccurred())
		_, err = goals.GetGoal(d, ctx, "o", "")
		Expect(err).To(HaveOccurred())
	})

	ginkgo.It("InvalidateGoalsForOwner: empty owner rejects; call for owner with no rows is a no-op", func() {
		d := openTestDBG()
		ctx := context.Background()
		Expect(goals.InvalidateGoalsForOwner(d, ctx, "")).To(HaveOccurred())

		// Non-owner: no-op with no error.
		u := mkSender("goals_invalid_none")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })
		Expect(goals.InvalidateGoalsForOwner(d, ctx, u)).To(Succeed())
	})

	ginkgo.It("CreateGoal: description=nil branch AND description=non-nil branch both round-trip", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("goal_desc")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM goals WHERE owner=$1`, u)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u)
		})
		spec := json.RawMessage(plantedSpec)

		g1, err := goals.CreateGoal(d, ctx, u, "nil-desc", nil, spec, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(g1.Description).To(BeNil())

		desc := "some description"
		g2, err := goals.CreateGoal(d, ctx, u, "with-desc", &desc, spec, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(g2.Description).NotTo(BeNil())
		Expect(*g2.Description).To(Equal(desc))
	})
})

var _ = ginkgo.Describe("input-validation error branches (gaka-d6x)", func() {

	// ---- goals.go ----

	ginkgo.It("CreateGoal: empty owner OR empty name OR empty spec all reject early with a Go error", func() {
		d := openTestDBG()
		ctx := context.Background()

		_, err := goals.CreateGoal(d, ctx, "", "n", nil, json.RawMessage(`{}`), false)
		Expect(err).To(HaveOccurred())
		_, err = goals.CreateGoal(d, ctx, "o", "", nil, json.RawMessage(`{}`), false)
		Expect(err).To(HaveOccurred())
		_, err = goals.CreateGoal(d, ctx, "o", "n", nil, nil, false)
		Expect(err).To(HaveOccurred())
		_, err = goals.CreateGoal(d, ctx, "o", "n", nil, json.RawMessage(``), false)
		Expect(err).To(HaveOccurred())
	})

	ginkgo.It("UpdateGoal: empty owner/id reject early; empty spec inside a patch rejects", func() {
		d := openTestDBG()
		ctx := context.Background()

		_, err := goals.UpdateGoal(d, ctx, "", "id", goals.GoalPatch{})
		Expect(err).To(HaveOccurred())
		_, err = goals.UpdateGoal(d, ctx, "o", "", goals.GoalPatch{})
		Expect(err).To(HaveOccurred())

		emptySpec := json.RawMessage(``)
		_, err = goals.UpdateGoal(d, ctx, "o", "id", goals.GoalPatch{Spec: &emptySpec})
		Expect(err).To(HaveOccurred(), "empty spec pointer must reject before SQL runs")
	})

	ginkgo.It("DeleteGoal / ToggleGoal reject empty owner OR id (no accidental match-all)", func() {
		d := openTestDBG()
		ctx := context.Background()
		_, err := goals.DeleteGoal(d, ctx, "", "id")
		Expect(err).To(HaveOccurred())
		_, err = goals.DeleteGoal(d, ctx, "o", "")
		Expect(err).To(HaveOccurred())

		_, _, err = goals.ToggleGoal(d, ctx, "", "id", nil)
		Expect(err).To(HaveOccurred())
		_, _, err = goals.ToggleGoal(d, ctx, "o", "", nil)
		Expect(err).To(HaveOccurred())
	})

	ginkgo.It("ToggleGoal on unknown id returns (false, false, nil) — never leak existence", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("goals_toggle_missing")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })

		newEnabled, found, err := goals.ToggleGoal(d, ctx, u, "00000000-0000-0000-0000-000000000000", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())
		Expect(newEnabled).To(BeFalse())
	})

	ginkgo.It("DeleteGoal on unknown id returns (false, nil) — never leak existence", func() {
		d := openTestDBG()
		ctx := context.Background()
		u := mkSender("goals_del_missing")
		Expect(insertFreshUser(d, ctx, u)).To(Succeed())
		ginkgo.DeferCleanup(func() { _, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, u) })
		ok, err := goals.DeleteGoal(d, ctx, u, "00000000-0000-0000-0000-000000000000")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
})
