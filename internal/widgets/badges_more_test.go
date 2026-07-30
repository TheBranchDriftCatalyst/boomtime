// badges_more_test.go — gaka-d6x.handler: cover BadgeLink + BadgeSvg (the
// public shields.io proxy path). Applied here (not badges_test.go, which is
// an in-package unit test of applyBadgeCuration) so the external harness
// can mock the shields.io upstream via net/http/httptest.
//
// Named invariants:
//
//	"BadgeLink requires auth" — unauth'd → 4xx. Auth'd → 200 + a
//	deterministic URL of the shape `<BadgeURL>/badge/svg/<uuid>`.
//
//	"BadgeSvg + hidden project → 404" — public curation is applied BEFORE
//	the shields.io fetch (a hidden project leaks nothing: label OR total).
//	Test flips a curation rule after link creation and expects 404.
//	CRITICAL: the shields.io stub upstream MUST NOT be called on the
//	hidden-project path — if it were, the badge PNG (with project name
//	as label) would be produced and served. Asserted via an atomic
//	counter that the stub records and the test verifies stayed at 0.
//
//	"BadgeSvg invalid id → 400 (not 500)" — an unparseable UUID is a
//	client mistake; the handler surfaces a clean 400 before any DB read.
//
//	"BadgeSvg on unknown UUID → 404" — well-formed but unknown id
//	produces a plain 404 (not a 500 leak of the DB miss).
//
//	"BadgeSvg upstream 502 does NOT leak upstream body" — the upstream
//	500 body contains a distinctive marker ("boom"); the client response
//	MUST NOT echo it. Companion to the LLM 502-no-leak assertion the
//	critique called out as pinned-in-user_avatar but missing here.
package widgets_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("BadgeLink (gaka-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/badge/link/alpha", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("auth'd caller receives {badgeUrl: '<BadgeURL>/badge/svg/<uuid>'}", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "https://example.test"
		e := hz.Router()
		user, token := hz.MintUser("badgeLink_ok")

		// FK (badges → projects); seed the project row first.
		hz.Seeder(user).Projects("alpha")

		rec := doJSONReqG(e, http.MethodGet, "/badge/link/alpha", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		body := rec.Body.String()
		Expect(body).To(ContainSubstring("https://example.test/badge/svg/"),
			"badgeUrl did not use the configured BadgeURL prefix: %s", body)
	})
})

var _ = Describe("BadgeSvg (gaka-d6x.handler)", func() {
	It("returns 400 for an unparseable UUID (fail-fast, no DB read)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/badge/svg/not-a-uuid", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"body=%s", rec.Body.String())
	})

	It("returns 404 for a well-formed but unknown UUID", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet,
			"/badge/svg/00000000-0000-0000-0000-000000000000", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"unknown badge id must 404; got body=%s", rec.Body.String())
	})

	It("proxies a public shields.io stub for a real badge and returns image/svg+xml", func() {
		// Spin up a fake shields.io that returns a tiny fixed body. This
		// pins the "success path" without touching the real internet in CI.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte("<svg>ok</svg>"))
		}))
		DeferCleanup(upstream.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "http://ignored.example"
		hz.Cfg.ShieldsIOURL = upstream.URL
		e := hz.Router()

		user, token := hz.MintUser("badgeSvg_ok")
		hz.Seeder(user).Projects("alpha")

		// Mint the link.
		mintRec := doJSONReqG(e, http.MethodGet, "/badge/link/alpha", token, nil)
		Expect(mintRec).To(testutil.HaveStatus(http.StatusOK), "mint: %s", mintRec.Body.String())
		var mintEnv struct {
			BadgeURL string `json:"badgeUrl"`
		}
		Expect(decodeJSONBody(mintRec.Body.Bytes(), &mintEnv)).To(Succeed())
		Expect(mintEnv.BadgeURL).NotTo(BeEmpty())

		// Extract the uuid suffix. The URL is `<BadgeURL>/badge/svg/<uuid>`.
		suffix := lastSegment(mintEnv.BadgeURL)
		Expect(suffix).NotTo(BeEmpty())

		// Public hit.
		req := httptest.NewRequest(http.MethodGet, "/badge/svg/"+suffix, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		Expect(rec.Header().Get("Content-Type")).To(Equal("image/svg+xml"))
		got, _ := io.ReadAll(rec.Body)
		Expect(string(got)).To(Equal("<svg>ok</svg>"))
	})

	It("hidden project → 404 with NO shields.io call AND no project-name leak (gaka-6jm.3)", func() {
		// Sentinel counter: the shields.io stub must NEVER be hit on the
		// hidden-project path. If it were, the SVG label would leak the
		// project name — which is precisely what applyBadgeCuration is
		// supposed to prevent.
		var upstreamHits atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstreamHits.Add(1)
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg>SHOULD_NEVER_APPEAR</svg>`))
		}))
		DeferCleanup(upstream.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "http://ignored.example"
		hz.Cfg.ShieldsIOURL = upstream.URL
		e := hz.Router()

		user, token := hz.MintUser("badgeSvg_hidden")
		hz.Seeder(user).Projects("secret-alpha")

		// Mint the link WHILE the project is still visible.
		mintRec := doJSONReqG(e, http.MethodGet, "/badge/link/secret-alpha", token, nil)
		Expect(mintRec).To(testutil.HaveStatus(http.StatusOK), "mint: %s", mintRec.Body.String())
		var mintEnv struct {
			BadgeURL string `json:"badgeUrl"`
		}
		Expect(decodeJSONBody(mintRec.Body.Bytes(), &mintEnv)).To(Succeed())
		suffix := lastSegment(mintEnv.BadgeURL)
		Expect(suffix).NotTo(BeEmpty())

		// Now hide 'secret-alpha' via the curation API — this creates a
		// hide rule on the project axis; LoadHiddenSets will return it
		// on the next lookup, and applyBadgeCuration will resolve
		// 'secret-alpha' → 'hidden' → BadgeSvg returns 404.
		hideRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation", token, map[string]any{
				"axis": "project", "action": "hide",
				"matchType": "exact", "matchValue": "secret-alpha",
			})
		Expect(hideRec).To(testutil.HaveStatus(http.StatusOK),
			"hide rule create: body=%s", hideRec.Body.String())

		// Sanity: LoadHiddenSets must now include this project (proves the
		// curation rule actually took effect on the read path). Guards
		// against a future refactor where the rule creation silently
		// filters out project-axis hides.
		hidden, err := hz.DB.LoadHiddenSets(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		var sawHide bool
		for _, p := range hidden.Projects() {
			if p == "secret-alpha" {
				sawHide = true
			}
		}
		Expect(sawHide).To(BeTrue(),
			"LoadHiddenSets did not include 'secret-alpha' after POST /curation — the hide rule didn't stick")

		// Public GET: hidden project → 404, no upstream call, no project name.
		req := httptest.NewRequest(http.MethodGet, "/badge/svg/"+suffix, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"hidden-project badge must 404; got %d body=%s", rec.Code, rec.Body.String())

		// The response body MUST NOT contain the project name (that would
		// leak curated data to an outsider — the whole point of the guard).
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret-alpha"),
			"hidden project name leaked in 404 body: %s", rec.Body.String())
		// And the shields.io upstream must NOT have been called.
		Expect(upstreamHits.Load()).To(Equal(int32(0)),
			"shields.io was called on the hidden-project path (SVG would leak project name in the label) — upstream hits=%d",
			upstreamHits.Load())
	})

	It("returns 502 when the shields.io upstream is broken AND does not leak upstream body", func() {
		// Upstream returns 500 with a distinctive body → handler must surface
		// 502 (bad gateway) and MUST NOT echo the "boom" marker verbatim.
		// The critique flagged this as a missing security assertion: prior
		// tests only checked the status code.
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom-shields-upstream-marker-abc123"))
		}))
		DeferCleanup(upstream.Close)

		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.BadgeURL = "http://ignored.example"
		hz.Cfg.ShieldsIOURL = upstream.URL
		e := hz.Router()
		user, token := hz.MintUser("badgeSvg_502")
		hz.Seeder(user).Projects("beta")

		mintRec := doJSONReqG(e, http.MethodGet, "/badge/link/beta", token, nil)
		Expect(mintRec).To(testutil.HaveStatus(http.StatusOK))
		var mintEnv struct {
			BadgeURL string `json:"badgeUrl"`
		}
		Expect(decodeJSONBody(mintRec.Body.Bytes(), &mintEnv)).To(Succeed())
		suffix := lastSegment(mintEnv.BadgeURL)

		req := httptest.NewRequest(http.MethodGet, "/badge/svg/"+suffix, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadGateway),
			"broken upstream should surface 502, got %d body=%s",
			rec.Code, rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("boom-shields-upstream-marker-abc123"),
			"shields.io upstream body leaked to client: %s", rec.Body.String())
	})
})
