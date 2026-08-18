// label_images_ginkgo_test.go — ginkgo mirror of label_images_test.go (gaka-0vp.13).
// 1:1 case map (4 stdlib TestXxx → 4 Its):
//
//	TestLabelImages_Roundtrip        → "roundtrip: save and read back"
//	TestLabelImages_Upsert           → "upsert: second save overwrites row"
//	TestLabelImages_NotFound         → "not found: missing id returns (nil,false,nil)"
//	TestLabelImages_HasLabelImage    → "HasLabelImage tracks save/delete"
package db

import (
	"bytes"
	"context"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("LabelImages", func() {
	ginkgo.It("roundtrips: save an image and read it back", func() {
		d := openTestDBG()
		ctx := context.Background()

		id := "test-late-night-coder"
		ginkgo.DeferCleanup(func() { _ = d.DeleteLabelImage(ctx, id) })

		img := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
		var seed int64 = 12345
		err := d.SaveLabelImage(ctx, id, img, "image/png", "flux_schnell_fast", "a distinctive emblem", &seed)
		Expect(err).NotTo(HaveOccurred())

		got, ok, err := d.GetLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "GetLabelImage: expected row, got miss")
		Expect(bytes.Equal(got.ImageBytes, img)).To(BeTrue(), "bytes mismatch: got %d bytes want %d bytes", len(got.ImageBytes), len(img))
		Expect(got.MimeType).To(Equal("image/png"))
		Expect(got.Model).To(Equal("flux_schnell_fast"))
		Expect(got.Prompt).To(Equal("a distinctive emblem"))
		Expect(got.Seed).NotTo(BeNil())
		Expect(*got.Seed).To(BeEquivalentTo(12345))
		Expect(got.GeneratedAt.IsZero()).To(BeFalse(), "GeneratedAt is zero — the DEFAULT now() didn't fire")
	})

	ginkgo.It("upserts: saving twice for the same id overwrites the row and never regresses generated_at", func() {
		d := openTestDBG()
		ctx := context.Background()

		id := "test-upsert"
		ginkgo.DeferCleanup(func() { _ = d.DeleteLabelImage(ctx, id) })

		err := d.SaveLabelImage(ctx, id, []byte("v1"), "image/png", "flux_schnell_fast", "prompt v1", nil)
		Expect(err).NotTo(HaveOccurred())
		first, _, err := d.GetLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())

		// Second save with different content; MUST overwrite, MUST bump generated_at.
		err = d.SaveLabelImage(ctx, id, []byte("v2-different-content"), "image/webp", "sdxl_illustrious_xl", "prompt v2", nil)
		Expect(err).NotTo(HaveOccurred())
		second, _, err := d.GetLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())

		Expect(string(second.ImageBytes)).To(Equal("v2-different-content"))
		Expect(second.MimeType).To(Equal("image/webp"))
		Expect(second.Model).To(Equal("sdxl_illustrious_xl"))
		// generated_at may be equal on a very fast test; the point is it never went backwards.
		Expect(second.GeneratedAt.Before(first.GeneratedAt)).To(BeFalse(),
			"generated_at regressed: first=%v second=%v", first.GeneratedAt, second.GeneratedAt)
	})

	ginkgo.It("returns (nil,false,nil) for a missing id", func() {
		d := openTestDBG()
		ctx := context.Background()

		li, ok, err := d.GetLabelImage(ctx, "does-not-exist-xyz")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(li).To(BeNil())
	})

	ginkgo.It("HasLabelImage flips true after Save, false after Delete", func() {
		d := openTestDBG()
		ctx := context.Background()

		id := "test-has"
		ginkgo.DeferCleanup(func() { _ = d.DeleteLabelImage(ctx, id) })

		has, err := d.HasLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(has).To(BeFalse(), "HasLabelImage true before Save")

		err = d.SaveLabelImage(ctx, id, []byte("x"), "image/png", "", "", nil)
		Expect(err).NotTo(HaveOccurred())
		has, err = d.HasLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(has).To(BeTrue(), "HasLabelImage false after Save")

		err = d.DeleteLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		has, err = d.HasLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(has).To(BeFalse(), "HasLabelImage true after Delete")
	})
})
