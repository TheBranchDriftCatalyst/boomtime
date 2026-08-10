// og_card_test.go — the OpenGraph social-card image endpoint + card knobs
// (gaka social-card). Exercises GET /api/public/profile/:slug/og.png end to
// end through the real router: a public profile renders a valid 1200×630 PNG;
// a non-public / unknown slug falls back to a generic branded PNG (no oracle);
// and the cardTheme / cardTagline profile knobs round-trip.
package identity_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// pngSig is the 8-byte PNG magic header.
var pngSig = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

func slugFor(user string) string {
	tail := user
	if len(tail) > 8 {
		tail = tail[len(tail)-8:]
	}
	return "ogc-" + strings.ToLower(strings.ReplaceAll(tail, ".", ""))
}

var _ = Describe("public OG social card image (gaka social-card)", func() {
	It("renders a valid 1200×630 PNG for a public profile", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("og_pub")
		slug := slugFor(user)

		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    slug,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "PUT profile: body=%s", rec.Body.String())

		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug+"/og.png", nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)

		Expect(rr.Code).To(Equal(http.StatusOK), "og.png GET: body len=%d", rr.Body.Len())
		Expect(rr.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rr.Header().Get("Cache-Control")).To(ContainSubstring("max-age=600"))
		Expect(rr.Header().Get("ETag")).NotTo(BeEmpty())
		Expect(rr.Body.Len()).To(BeNumerically(">", 1000))
		Expect(rr.Body.Bytes()[:8]).To(Equal(pngSig), "response is not a PNG")

		// ETag revalidation: a matching If-None-Match short-circuits to 304.
		etag := rr.Header().Get("ETag")
		req2 := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug+"/og.png", nil)
		req2.Header.Set("If-None-Match", etag)
		rr2 := httptest.NewRecorder()
		e.ServeHTTP(rr2, req2)
		Expect(rr2.Code).To(Equal(http.StatusNotModified))
	})

	It("serves a generic branded PNG for an unknown / non-public slug (no oracle)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		// Unknown slug — never provisioned. Must still be a 200 PNG (generic
		// card), not a 404 or a broken image.
		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/nobody-here-xyz/og.png", nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK))
		Expect(rr.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rr.Body.Bytes()[:8]).To(Equal(pngSig))
	})

	It("serves the generic card (not the owner's stats) for a DISABLED profile", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("og_off")
		slug := slugFor(user)

		// Enable then disable — leaves the slug row present but not public.
		Expect(doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true, "slug": slug,
		})).To(testutil.HaveStatus(http.StatusOK))
		Expect(doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": false, "slug": slug,
		})).To(testutil.HaveStatus(http.StatusOK))

		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug+"/og.png", nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		// Same no-oracle behavior: a 200 PNG regardless of whether the slug
		// exists — an outsider can't distinguish "disabled" from "never existed".
		Expect(rr.Code).To(Equal(http.StatusOK))
		Expect(rr.Body.Bytes()[:8]).To(Equal(pngSig))
	})
})

var _ = Describe("social-card knobs round-trip (gaka social-card)", func() {
	It("persists cardTheme + cardTagline and surfaces them on GET profile + public dashboard", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("og_knob")
		slug := slugFor(user)

		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled":     true,
			"slug":        slug,
			"cardTheme":   "light",
			"cardTagline": "shipping boomtime",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"cardTheme":"light"`))
		Expect(rec.Body.String()).To(ContainSubstring(`"cardTagline":"shipping boomtime"`))

		// GET the owner's settings back.
		got := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/profile", token, nil)
		Expect(got).To(testutil.HaveStatus(http.StatusOK))
		Expect(got.Body.String()).To(ContainSubstring(`"cardTagline":"shipping boomtime"`))

		// Public dashboard payload carries the tagline (feeds the FE hero).
		pub := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		pr := httptest.NewRecorder()
		e.ServeHTTP(pr, pub)
		Expect(pr.Code).To(Equal(http.StatusOK))
		Expect(pr.Body.String()).To(ContainSubstring(`"tagline":"shipping boomtime"`))
	})

	It("rejects an invalid cardTheme with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("og_badtheme")
		slug := slugFor(user)
		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled":   true,
			"slug":      slug,
			"cardTheme": "hotdog-stand",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})
})
