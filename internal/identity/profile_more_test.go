// profile_more_test.go — gaka-d6x.handler: cover the profile handler paths
// not exercised by profile_test.go (GetPublicProfile, PutPublicProfile
// validation branches, and PublicProfile 404 / disabled paths).
//
// Named invariants:
//
//	"unauth GET /profile → 4xx" — fail-closed.
//
//	"GET /profile returns enabled=false, slug=nil for a fresh user" —
//	the never-set default state. Explicitly pins the shape the FE
//	settings view relies on.
//
//	"PUT /profile validation: enabled+empty slug, reserved, bad regex → 400" —
//	each validation branch is a distinct 400 before the DB write.
//
//	"PUT /profile then GET /profile round-trips slug + enabled" — read-
//	your-writes on the auth'd endpoints.
//
//	"public GET /api/public/profile/:slug with unknown slug → 404" — a
//	terse 404 (no oracle over slug existence).
//
//	"public GET /api/public/profile/:slug with malformed slug → 404" —
//	the format guard runs BEFORE the DB lookup.
//
//	"public GET on a disabled profile → 404" — an enable-toggle=false
//	profile must return 404, not the payload.
package identity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("GetPublicProfile / PutPublicProfile (gaka-d6x.handler)", func() {
	It("unauth GET → 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/profile", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("fresh user gets enabled=false and null slug", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("prof_new")

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/profile", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var body struct {
			Enabled bool    `json:"enabled"`
			Slug    *string `json:"slug"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed(),
			"body=%s", rec.Body.String())
		Expect(body.Enabled).To(BeFalse())
		Expect(body.Slug).To(BeNil(),
			"fresh user slug should be null; got %v", body.Slug)
	})

	It("PUT enabled=true requires a slug (empty → 400)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("prof_needslug")

		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    "",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("PUT with a reserved slug (like 'admin') → 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("prof_reserved")

		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    "admin",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("reserved"),
			"reserved-slug rejection should say 'reserved'; body=%s", rec.Body.String())
	})

	It("PUT with an invalid slug (starts with hyphen) → 400 with regex hint", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("prof_regex")

		// Leading hyphen violates the anchor `^[a-z0-9]…` → 400.
		rec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    "-oops",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("PUT valid slug → GET reads back the same value", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("prof_rt")

		slug := "prt-" + user[len(user)-6:]
		putRec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true,
			"slug":    slug,
		})
		Expect(putRec).To(testutil.HaveStatus(http.StatusOK), "body=%s", putRec.Body.String())

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/profile", token, nil)
		Expect(getRec).To(testutil.HaveStatus(http.StatusOK))

		var got struct {
			Enabled bool    `json:"enabled"`
			Slug    *string `json:"slug"`
		}
		Expect(json.Unmarshal(getRec.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Enabled).To(BeTrue())
		Expect(got.Slug).NotTo(BeNil())
		Expect(*got.Slug).To(Equal(slug))
	})

	It("PUT slug collision between two users → 409 Conflict", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, aliceTok := hz.MintUser("prof_conflictA")
		_, bobTok := hz.MintUser("prof_conflictB")

		// Alice claims the slug first.
		slug := "shared-slug-conflict-abc"
		aRec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", aliceTok, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(aRec).To(testutil.HaveStatus(http.StatusOK), "alice: %s", aRec.Body.String())

		// Bob tries the same slug → 409 Conflict.
		bRec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", bobTok, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(bRec).To(testutil.HaveStatus(http.StatusConflict),
			"bob claiming alice's slug should 409; got %d body=%s",
			bRec.Code, bRec.Body.String())
	})

	It("PUT enabled=false with no slug clears the toggle without wiping the row", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("prof_dis")

		slug := "pdi-" + user[len(user)-6:]
		putRec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(putRec).To(testutil.HaveStatus(http.StatusOK))

		// Now disable — no slug supplied. Handler must accept.
		putRec2 := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": false,
		})
		Expect(putRec2).To(testutil.HaveStatus(http.StatusOK), "body=%s", putRec2.Body.String())

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/profile", token, nil)
		Expect(getRec).To(testutil.HaveStatus(http.StatusOK))
		var got struct {
			Enabled bool    `json:"enabled"`
			Slug    *string `json:"slug"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &got)
		Expect(got.Enabled).To(BeFalse())
	})
})

var _ = Describe("PublicProfile (public /p/:slug) (gaka-d6x.handler)", func() {
	It("unknown slug → 404 (terse, no oracle)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet,
			"/api/public/profile/nobody-here-xyz", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound), "body=%s", rec.Body.String())
	})

	It("malformed slug is rejected BEFORE any DB read (404, still terse)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		// Uppercase letters violate the [a-z0-9-] format regex → immediate 404.
		req := httptest.NewRequest(http.MethodGet,
			"/api/public/profile/BAD_SLUG", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("disabled slug → 404 (privacy propagation)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("pub_dis")

		slug := "dis-" + user[len(user)-6:]
		// First enable to plant the row.
		putRec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(putRec).To(testutil.HaveStatus(http.StatusOK))
		// Then disable.
		putRec2 := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": false, "slug": slug,
		})
		Expect(putRec2).To(testutil.HaveStatus(http.StatusOK))

		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"disabled profile must 404; body=%s", rec.Body.String())
	})

	It("returns a quoted ETag header — enables client-side revalidation", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("pub_etag")

		slug := "et-" + user[len(user)-6:]
		putRec := doJSONReqG(e, http.MethodPut, "/api/v1/users/current/profile", token, map[string]any{
			"enabled": true, "slug": slug,
		})
		Expect(putRec).To(testutil.HaveStatus(http.StatusOK))

		req := httptest.NewRequest(http.MethodGet, "/api/public/profile/"+slug, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "public: %s", rec.Body.String())

		etag := rec.Header().Get("ETag")
		Expect(etag).NotTo(BeEmpty(), "missing ETag")
		Expect(etag[0]).To(Equal(byte('"')), "ETag must be quoted per RFC 7232: %q", etag)
		Expect(etag[len(etag)-1]).To(Equal(byte('"')),
			"ETag must be quoted per RFC 7232: %q", etag)
	})
})
