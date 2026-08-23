// frame_style_hash_test.go — byte-identity pin for the extracted frame CSS
// (boom-8tn.1).
//
// Before boom-8tn.1 the shared <style> block lived inline in frame.go as a
// raw-string. This test asserts the bytes served by every OpenFrame call are
// byte-identical to the pre-refactor literal, so that:
//
//  1. The one-shot extraction to //go:embed frame_style.css produced
//     BYTE-IDENTICAL output (hash below computed on main @ 275ee1c from the
//     raw literal inside frame.go's OpenFrame).
//  2. A future edit to frame_style.css will trip this test and force an
//     intentional hash update rather than silent CSS drift into every SVG
//     widget every user embeds on their README.
//
// If you legitimately edit frame_style.css, recompute with:
//
//	shasum -a 256 internal/widget/frame_style.css
package widget

import (
	"crypto/sha256"
	"encoding/hex"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Pinned pre-refactor hash — computed on main @ 275ee1c by extracting the
// raw-literal `<style>...</style>` inside OpenFrame and running shasum -a 256.
const frameStyleSHA256 = "3511857b64ade67717207ee34d871bb81fefa41fa7b3aa78dfb0c967aba6c281"

var _ = Describe("widget.frameStyleCSS bytes (boom-8tn.1)", func() {
	It("has SHA-256 matching the pre-refactor pin", func() {
		sum := sha256.Sum256(frameStyleCSS)
		Expect(hex.EncodeToString(sum[:])).To(Equal(frameStyleSHA256),
			"frame_style.css drifted from pre-refactor bytes — either the "+
				"extraction to //go:embed dropped bytes, or the CSS was edited "+
				"without updating the pinned hash in this test")
		Expect(len(frameStyleCSS)).To(Equal(788),
			"frame CSS byte-count drift — check for stray CRLF or trailing newline")
	})

	It("is emitted verbatim by OpenFrame (i.e. every rendered widget still carries the shared animations)", func() {
		th := themeFor("dark")
		f := OpenFrame(100, 100, th, "smoke", "")
		out := string(f.Close())
		// The pinned CSS block must appear as a contiguous substring.
		Expect(out).To(ContainSubstring(string(frameStyleCSS)),
			"OpenFrame stopped emitting the shared CSS block verbatim — "+
				"either f.buf.Write(frameStyleCSS) got dropped or an extra wrapper snuck in")
	})
})
