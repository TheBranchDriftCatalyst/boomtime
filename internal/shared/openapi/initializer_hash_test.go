// initializer_hash_test.go — byte-identity pin for the extracted
// swagger-initializer.js (boom-8tn.1).
//
// Before boom-8tn.1 the initializer JS lived as a 1000-LOC raw-string
// constant inside ui.go. This test pins the SHA-256 of what UIHandler
// actually serves at /swagger-initializer.js so that:
//
//  1. The one-shot extraction to //go:embed initializer.js produced
//     BYTE-IDENTICAL output (the pre-refactor hash below was computed on
//     main @ 275ee1c by reconstituting the raw literal — replacing every
//     `+ "`" + ` splice with a real backtick — and sha256'ing).
//  2. Any FUTURE edit to initializer.js will fail this test and force
//     the author to intentionally update the pinned hash — guarding against
//     accidental byte-drift in a file that ships to every browser.
//
// If you legitimately edit initializer.js, recompute the hash with:
//
//	shasum -a 256 internal/openapi/initializer.js
//
// and update `initializerSHA256` below (and update the endpoint-served
// hash too — they're the same file, but the test proves the handler is
// serving the extracted bytes verbatim).
package openapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/openapi"
)

// Pinned pre-refactor hash — computed on main @ 275ee1c by extracting the
// inline `const initializerJS = ...` raw literal, resolving the two `+ "`"
// + ` backtick-splices, and running `shasum -a 256`. If this test fails,
// EITHER initializer.js drifted (intentional: update this hash) OR the
// extraction dropped bytes (unintentional: bisect and restore).
const initializerSHA256 = "997c2ecb1371edb7cf86fd209a3f3472c5fa15618c3e801c892a67a8d33e0073"

var _ = Describe("openapi.UIHandler served initializer bytes (boom-8tn.1)", func() {
	It("serves swagger-initializer.js whose SHA-256 matches the pre-refactor pin", func() {
		h := openapi.UIHandler("/api/docs")
		req := httptest.NewRequest(http.MethodGet, "/api/docs/swagger-initializer.js", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		body, err := io.ReadAll(rec.Result().Body)
		Expect(err).NotTo(HaveOccurred())

		sum := sha256.Sum256(body)
		got := hex.EncodeToString(sum[:])
		Expect(got).To(Equal(initializerSHA256),
			"served /swagger-initializer.js bytes drifted from the pre-refactor pin — "+
				"either the extraction to //go:embed dropped bytes, or initializer.js "+
				"was edited without updating the pinned hash in this test")
		// Also pin the exact byte count. Byte-identity is what we care about
		// but a size drift is a faster hint at what changed.
		Expect(len(body)).To(Equal(49668),
			"served initializer size drift — check for stray CRLF conversions or extra bytes")
	})
})
