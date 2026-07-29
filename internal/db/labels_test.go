// labels_ginkgo_test.go — ginkgo mirror of labels_test.go (gaka-0vp.13).
// 1:1 case map (6 stdlib TestXxx + 4 subtests → 6 Its + 1 DescribeTable(4)):
//
//	TestLabels_ListSeeded                    → It "ListLabels: seeded ids + condition JSONB integrity"
//	TestLabels_UpsertRoundtrip               → It "Upsert + Get roundtrips all editable fields"
//	TestLabels_UpsertRejectsBadInput         → DescribeTable "Upsert rejects bad input" (4 entries)
//	TestLabels_CheckConstraintRejectsBadKind → It "CHECK constraint rejects unknown kind"
//	TestLabels_DeleteIdempotent              → It "DeleteLabel is idempotent"
//	TestLabelGenConfig_Roundtrip             → It "GenConfig get/set roundtrip"
package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("labels", func() {
	ginkgo.It("ListLabels returns the seeded ids with valid condition JSONB", func() {
		d := openTestDBG()
		ctx := context.Background()

		labels, err := d.ListLabels(ctx)
		Expect(err).NotTo(HaveOccurred())

		byID := map[string]Label{}
		for _, l := range labels {
			byID[l.ID] = l
		}
		wantIDs := []struct {
			id   string
			kind string
		}{
			{"languages-python-legend", "tier"},
			{"editors-vim-master", "tier"},
			{"late-night-coder", "archetype"},
			{"machine", "archetype"},
			{"vim-enjoyer", "tribe"},
			{"cross-platform", "tribe"},
			{"commander-neko-paws", "meme"},
			{"sigma-grindset", "meme"},
			{"based-chad-ultimate", "meme"},
		}
		for _, w := range wantIDs {
			l, ok := byID[w.id]
			Expect(ok).To(BeTrue(), "seed missing id=%s", w.id)
			Expect(l.Kind).To(Equal(w.kind), "%s kind", w.id)
		}

		for _, l := range labels {
			var probe map[string]any
			err := json.Unmarshal(l.Condition, &probe)
			Expect(err).NotTo(HaveOccurred(), "%s: condition unmarshal", l.ID)
			_, ok := probe["kind"]
			Expect(ok).To(BeTrue(), "%s: condition has no `kind` discriminant: %s", l.ID, string(l.Condition))
		}
	})

	ginkgo.It("Upsert + Get roundtrips every editable field and preserves created_at across updates", func() {
		d := openTestDBG()
		ctx := context.Background()

		id := "test-upsert-label"
		ginkgo.DeferCleanup(func() { _ = d.DeleteLabel(ctx, id) })

		cond := json.RawMessage(`{"kind":"all","of":[{"kind":"axis-time","axis":"languages","value":"python","op":">=","hours":5},{"kind":"streak","which":"current","op":">=","days":10}]}`)
		orig := Label{
			ID:              id,
			Kind:            "archetype",
			Label:           "TEST ROUNDTRIP",
			Glyph:           "★",
			Description:     "roundtrip fixture",
			OptimizedPrompt: "cyberpunk emblem",
			Rank:            77,
			Tier:            "",
			Condition:       cond,
		}
		Expect(d.UpsertLabel(ctx, orig)).To(Succeed())

		first, err := d.GetLabel(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).NotTo(BeNil())

		Expect(first.Kind).To(Equal("archetype"))
		Expect(first.Label).To(Equal("TEST ROUNDTRIP"))
		Expect(first.Glyph).To(Equal("★"))
		Expect(first.Description).To(Equal("roundtrip fixture"))
		Expect(first.OptimizedPrompt).To(Equal("cyberpunk emblem"))
		Expect(first.Rank).To(Equal(77))

		var got, want any
		Expect(json.Unmarshal(first.Condition, &got)).To(Succeed())
		Expect(json.Unmarshal(cond, &want)).To(Succeed())
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		Expect(string(gotJSON)).To(Equal(string(wantJSON)), "condition drift")

		newCond := json.RawMessage(`{"kind":"daily-avg","op":">=","hours":8}`)
		updated := orig
		updated.Rank = 999
		updated.OptimizedPrompt = "updated cyberpunk emblem"
		updated.Condition = newCond
		Expect(d.UpsertLabel(ctx, updated)).To(Succeed())
		second, err := d.GetLabel(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Rank).To(Equal(999))
		Expect(second.OptimizedPrompt).To(Equal("updated cyberpunk emblem"))
		Expect(second.UpdatedAt.Before(first.UpdatedAt)).To(BeFalse(), "updated_at regressed")
		Expect(second.CreatedAt.Equal(first.CreatedAt)).To(BeTrue(), "created_at drifted on update")
	})

	ginkgo.DescribeTable("Upsert rejects structurally-invalid input",
		func(mut func(l *Label)) {
			d := openTestDBG()
			ctx := context.Background()
			good := Label{ID: "x", Kind: "archetype", Label: "X",
				Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`)}
			bad := good
			mut(&bad)
			Expect(d.UpsertLabel(ctx, bad)).To(HaveOccurred())
		},
		ginkgo.Entry("empty id", func(l *Label) { l.ID = "" }),
		ginkgo.Entry("empty kind", func(l *Label) { l.Kind = "" }),
		ginkgo.Entry("empty label", func(l *Label) { l.Label = "" }),
		ginkgo.Entry("empty condition", func(l *Label) { l.Condition = nil }),
	)

	ginkgo.It("CHECK constraint on `kind` rejects unknown values at the DB layer", func() {
		d := openTestDBG()
		ctx := context.Background()

		bad := Label{ID: "test-bad-kind", Kind: "bogus", Label: "X",
			Condition: json.RawMessage(`{"kind":"daily-avg","op":">=","hours":1}`)}
		err := d.UpsertLabel(ctx, bad)
		if err == nil {
			_ = d.DeleteLabel(ctx, "test-bad-kind")
			ginkgo.Fail("expected CHECK constraint violation on kind='bogus'")
		}
		if !strings.Contains(err.Error(), "labels_kind_check") && !strings.Contains(err.Error(), "check") {
			ginkgo.Fail("expected check constraint error; got " + err.Error())
		}
	})

	ginkgo.It("DeleteLabel on a missing id is idempotent (returns nil)", func() {
		d := openTestDBG()
		ctx := context.Background()

		Expect(d.DeleteLabel(ctx, "definitely-does-not-exist-xyz")).To(Succeed())
	})

	ginkgo.It("GenConfig get/set roundtrips the systemPrompt string", func() {
		d := openTestDBG()
		ctx := context.Background()

		seeded, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = d.SetGenConfig(ctx, seeded) })

		newPrompt := "test system prompt from unit test — do not ship"
		Expect(d.SetGenConfig(ctx, newPrompt)).To(Succeed())
		got, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(newPrompt))
	})
})
