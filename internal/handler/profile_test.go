// profile_ginkgo_test.go — ginkgo mirror of profile_test.go (gaka-6jm.1/.12/bi2).
// 1:1 case map (2 stdlib TestXxx):
//   TestPublicProfileCacheHeadersTightPolicy → public profile cache headers > "tight policy (max-age=60, must-revalidate, no s-maxage, quoted ETag)"
//   TestPutPublicProfile_BodySizeCap_413     → PutPublicProfile body-size cap > "5 KiB body → 413 before slug regex runs"
package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// routerWithPublicProfileG — mirror of the stdlib file's routerWithPublicProfile.
var _ = Describe("public profile cache headers (gaka-6jm.12)", func() {
	It("advertises tight policy — max-age=60, must-revalidate, no s-maxage, quoted ETag", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cache_hdr_g")

		slug := "cachehdrg-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    slug,
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "PUT profile: body=%s", rec.Body.String())

		// Hit the public route.
		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		rr := httptest.NewRecorder()
		e.ServeHTTP(rr, req)
		Expect(rr.Code).To(Equal(http.StatusOK), "public profile GET: body=%s", rr.Body.String())

		cc := rr.Header().Get("Cache-Control")
		Expect(cc).To(ContainSubstring("must-revalidate"))
		Expect(cc).To(ContainSubstring("max-age=60"))
		Expect(cc).NotTo(ContainSubstring("s-maxage"),
			"Cache-Control still advertises s-maxage (cache leak regression)")
		Expect(cc).NotTo(ContainSubstring("max-age=300"),
			"Cache-Control still advertises the old 5-minute window")

		etag := rr.Header().Get("ETag")
		Expect(etag).NotTo(BeEmpty(), "missing ETag → revalidation would be full-body")
		Expect(strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`)).To(BeTrue(),
			"ETag not quoted per RFC 7232: %q", etag)

		// Mismatched If-None-Match must still 200 (the branch respects headers,
		// doesn't blanket-304).
		req2 := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		req2.Header.Set("If-None-Match", `"deadbeefdeadbeef"`)
		rr2 := httptest.NewRecorder()
		e.ServeHTTP(rr2, req2)
		Expect(rr2.Code).To(Equal(http.StatusOK),
			"If-None-Match with wrong ETag: must not blanket-304")
	})
})

var _ = Describe("PutPublicProfile body-size cap (gaka-bi2)", func() {
	It("rejects a 5 KiB body with 413 before slug regex runs", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("prof_413_g")

		big := strings.Repeat("a", 5000)
		body := []byte(`{"enabled":true,"slug":"` + big + `"}`)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/current/profile", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge),
			"400 would prove the slug regex ran on the payload — cap should fire first. body=%s",
			rec.Body.String())
	})
})
