// user_avatars_ginkgo_test.go — ginkgo mirror of user_avatars_test.go (gaka-0vp.13).
// 1:1 case map (5 stdlib TestXxx → 5 Its):
//
//	TestUserAvatars_SaveRoundtrip       → "SaveUserAvatar > round-trips ready row"
//	TestUserAvatars_StatusTransitions   → "SetAvatarStatus > running/error/retry transitions"
//	TestUserAvatars_ErrorPreservesBytes → "SetAvatarStatus(error) > preserves image_bytes"
//	TestUserAvatars_UnknownStatus       → "SetAvatarStatus > rejects unknown status"
//	TestUserAvatars_NotFound            → "GetUserAvatar > returns (nil,false,nil) miss"
package db

import (
	"bytes"
	"context"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("user_avatars", func() {
	ginkgo.It("SaveUserAvatar round-trips a ready row with all columns intact", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("useravatar_save")
		cleanupSenderG(d, ctx, sender)
		ensureUserG(d, ctx, sender)

		img := []byte("\x89PNG\r\n\x1a\nfake-chibi-portrait-bytes")
		var seed int64 = 424242
		err := d.SaveUserAvatar(ctx, sender, img, "image/png",
			"chroma_hd", "a chibi hacker in a hoodie, neon glow", &seed)
		Expect(err).NotTo(HaveOccurred())

		got, ok, err := d.GetUserAvatar(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "GetUserAvatar: expected row, got miss")
		Expect(bytes.Equal(got.ImageBytes, img)).To(BeTrue())
		Expect(got.MimeType).To(Equal("image/png"))
		Expect(got.Model).To(Equal("chroma_hd"))
		Expect(got.Prompt).NotTo(BeEmpty())
		Expect(got.Seed).NotTo(BeNil())
		Expect(*got.Seed).To(Equal(seed))
		Expect(got.Status).To(Equal(UserAvatarStatusReady))
		Expect(got.GeneratedAt).NotTo(BeNil(), "SaveUserAvatar should stamp now()")
	})

	ginkgo.It("SetAvatarStatus drives running/error/retry transitions and clears prior error on retry", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("useravatar_status")
		cleanupSenderG(d, ctx, sender)
		ensureUserG(d, ctx, sender)

		// pre-condition: no row.
		info, ok, err := d.GetUserAvatarStatus(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(info).To(BeNil())

		// running (creates the row via upsert).
		Expect(d.SetAvatarStatus(ctx, sender, UserAvatarStatusRunning, "")).To(Succeed())
		info, ok, err = d.GetUserAvatarStatus(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(info.Status).To(Equal(UserAvatarStatusRunning))
		Expect(info.ErrorMessage).To(BeEmpty())

		// error.
		Expect(d.SetAvatarStatus(ctx, sender, UserAvatarStatusError, "shim: 503 upstream unavailable")).To(Succeed())
		info, _, err = d.GetUserAvatarStatus(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Status).To(Equal(UserAvatarStatusError))
		Expect(info.ErrorMessage).NotTo(BeEmpty(), "expected the shim 503 message")

		// running again (retry) — MUST clear the prior error_message.
		Expect(d.SetAvatarStatus(ctx, sender, UserAvatarStatusRunning, "")).To(Succeed())
		info, _, err = d.GetUserAvatarStatus(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.ErrorMessage).To(BeEmpty(), "retry into 'running' should clear the prior error")
	})

	ginkgo.It("SetAvatarStatus(error) preserves the previously-ready image_bytes", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("useravatar_err_preserves")
		cleanupSenderG(d, ctx, sender)
		ensureUserG(d, ctx, sender)

		img := []byte("original-good-image")
		Expect(d.SaveUserAvatar(ctx, sender, img, "image/png", "m", "p", nil)).To(Succeed())
		Expect(d.SetAvatarStatus(ctx, sender, UserAvatarStatusError, "shim timeout")).To(Succeed())
		got, ok, err := d.GetUserAvatar(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(bytes.Equal(got.ImageBytes, img)).To(BeTrue(),
			"bytes wiped by SetAvatarStatus(error); regression: FE would lose the old avatar during a retry")
		Expect(got.Status).To(Equal(UserAvatarStatusError))
	})

	ginkgo.It("SetAvatarStatus rejects unknown status strings", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := mkSender("useravatar_unknown")
		cleanupSenderG(d, ctx, sender)
		ensureUserG(d, ctx, sender)

		Expect(d.SetAvatarStatus(ctx, sender, UserAvatarStatus("bogus"), "")).To(HaveOccurred())
	})

	ginkgo.It("GetUserAvatar returns (nil,false,nil) for a missing user", func() {
		d := openTestDBG()
		ctx := context.Background()

		got, ok, err := d.GetUserAvatar(ctx, "does-not-exist-xyz-9v4")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		Expect(got).To(BeNil())
	})
})
