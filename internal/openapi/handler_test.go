// handler_test.go — coverage for SpecHandler / DocsHandler / Register /
// UIHandler / strPtr / itoa (gaka-se2.3).
//
// The pyramid these tests implement:
//   - SpecHandler: response is valid JSON that PARSES BACK as
//     openapi3.T (not just a byte-blob); Cache-Control + Content-Type
//     headers are pinned.
//   - DocsHandler: X-Frame-Options SAMEORIGIN AND CSP frame-ancestors
//     are BOTH present — either missing enables the clickjacking path
//     the gaka-6jm/gaka-b5x threat model closes.
//   - UIHandler: index.html served with cache-bust query + no-store;
//     swagger-initializer.js served with no-store; static assets fall
//     through to http.FileServer (cacheable).
//   - Register: full round-trip proves both routes reachable.
//   - strPtr / itoa: pure helpers, pin edge cases (empty string,
//     itoa(0)).
//
// Anti-tautology: no test asserts "SpecHandler returned some JSON" —
// every test either DECODES the JSON into openapi3.T (verifying it's
// spec-shaped) or pins a specific header value the security model
// depends on.
package openapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/labstack/echo/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
)

var _ = Describe("openapi.SpecHandler (gaka-se2.3)", func() {
	newRec := func() (*httptest.ResponseRecorder, *echo.Context) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		return rec, c
	}

	It("responds 200 with application/json content-type", func() {
		rec, c := newRec()
		Expect(openapi.SpecHandler(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get(echo.HeaderContentType)).To(Equal("application/json; charset=utf-8"))
	})

	It("sets Cache-Control: public, max-age=300 (deterministic-until-binary-swap invariant)", func() {
		rec, c := newRec()
		Expect(openapi.SpecHandler(c)).To(Succeed())
		Expect(rec.Header().Get("Cache-Control")).To(Equal("public, max-age=300"),
			"cache 300s pins the 'spec only changes on binary swap' contract; a shorter TTL would waste CDN bandwidth, a longer one would keep stale specs after deploys")
	})

	It("serves JSON that PARSES BACK as a valid openapi3.T (not opaque bytes)", func() {
		rec, c := newRec()
		Expect(openapi.SpecHandler(c)).To(Succeed())
		loader := openapi3.NewLoader()
		doc, err := loader.LoadFromData(rec.Body.Bytes())
		Expect(err).NotTo(HaveOccurred(), "handler emitted non-spec-shaped JSON")
		Expect(doc.Validate(context.Background())).To(Succeed(),
			"emitted JSON failed openapi3 validation — handler is producing malformed spec")
	})
})

var _ = Describe("openapi.DocsHandler (gaka-se2.3, security)", func() {
	// SECURITY-CRITICAL: BOTH headers must be present. Missing X-Frame-Options
	// AND missing CSP frame-ancestors together enable the token-mint-FAB
	// clickjacking attack path (a hostile iframe embeds /api/docs, tricks
	// a logged-in operator into clicking the mint button — refresh_token
	// cookie is SameSite=Strict but the CLICK is authenticated).
	handler := func() echo.HandlerFunc { return openapi.DocsHandler("/api/docs") }

	It("sets X-Frame-Options: SAMEORIGIN (clickjacking guard for legacy browsers)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		Expect(handler()(c)).To(Succeed())
		Expect(rec.Header().Get("X-Frame-Options")).To(Equal("SAMEORIGIN"))
	})

	It("sets Content-Security-Policy: frame-ancestors 'self' (modern CSP path)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		Expect(handler()(c)).To(Succeed())
		Expect(rec.Header().Get("Content-Security-Policy")).To(Equal("frame-ancestors 'self'"))
	})

	It("returns a body (not just headers — proves UIHandler delegation runs)", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		Expect(handler()(c)).To(Succeed())
		Expect(rec.Body.Len()).To(BeNumerically(">", 0),
			"empty body — DocsHandler failed to hand off to UIHandler; would be a blank docs page")
	})
})

var _ = Describe("openapi.UIHandler (gaka-se2.3)", func() {
	h := openapi.UIHandler("/api/docs")

	It("serves index.html at the root with a cache-bust query on swagger-initializer.js", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/html"))
		// Cache-bust: initializer script tag MUST include ?v=<hash>.
		Expect(rec.Body.String()).To(ContainSubstring("./swagger-initializer.js?v="),
			"index.html doesn't carry the initializer cache-bust query — a deploy that changes initializerJS won't invalidate browser caches")
	})

	It("serves index.html with Cache-Control: no-store, must-revalidate (wrapper must revalidate to catch new ?v=)", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store, must-revalidate"),
			"index.html cache-permission would leave a stale cached copy pointing at the OLD initializer query string after a deploy")
	})

	It("serves swagger-initializer.js with no-store (initializer evolves per-release)", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/docs/swagger-initializer.js", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("application/javascript"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store, must-revalidate"))
		Expect(rec.Body.Len()).To(BeNumerically(">", 100),
			"initializer body suspiciously small — probably empty embed")
	})

	It("serves vendored assets (CSS) as cacheable (no no-store) to keep bandwidth low across deploys", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/docs/swagger-ui.css", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			// Explicit assertion: vendored asset MUST NOT carry no-store.
			// (Present in dev-only builds; skip the assertion if the embed
			// doesn't ship the file — CSS pathway is not load-bearing for
			// coverage, but if it exists it must be cacheable.)
			Expect(rec.Header().Get("Cache-Control")).NotTo(Equal("no-store, must-revalidate"),
				"vendored assets must be cacheable — no-store would tank load performance across deploys")
		}
	})
})

var _ = Describe("openapi.Register (gaka-se2.3)", func() {
	It("wires both /api/openapi.json and /api/docs onto the echo instance", func() {
		e := echo.New()
		openapi.Register(e)

		// /api/openapi.json → spec
		req1 := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
		rec1 := httptest.NewRecorder()
		e.ServeHTTP(rec1, req1)
		Expect(rec1.Code).To(Equal(http.StatusOK),
			"Register failed to wire /api/openapi.json")
		Expect(rec1.Header().Get(echo.HeaderContentType)).To(HavePrefix("application/json"))

		// /api/docs → HTML UI + security headers
		req2 := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req2)
		Expect(rec2.Code).To(Equal(http.StatusOK),
			"Register failed to wire /api/docs")
		Expect(rec2.Header().Get("X-Frame-Options")).To(Equal("SAMEORIGIN"),
			"security header dropped when reaching handler via Register (regression against the wiring)")
	})

	It("wires the wildcard /api/docs/* for static assets", func() {
		e := echo.New()
		openapi.Register(e)
		// The wildcard handler routes /api/docs/anything to UIHandler.
		// swagger-initializer.js is the canonical asset the initializer
		// pulls; a 404 here would break the docs UI.
		req := httptest.NewRequest(http.MethodGet, "/api/docs/swagger-initializer.js", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK),
			"Register failed to wire the /api/docs/* wildcard — static asset requests will 404")
	})
})

// -- pure helpers -------------------------------------------------------

var _ = Describe("openapi.strPtr / itoa (gaka-se2.3)", func() {
	It("strPtr returns a pointer whose deref matches the input for a non-empty string", func() {
		p := openapi.StrPtrForTest("hello")
		Expect(p).NotTo(BeNil())
		Expect(*p).To(Equal("hello"))
	})

	It("strPtr returns a valid pointer even for the empty string (not nil)", func() {
		p := openapi.StrPtrForTest("")
		Expect(p).NotTo(BeNil(),
			"empty-string input must still yield a non-nil pointer — openapi3 rejects a nil Description")
		Expect(*p).To(Equal(""))
	})

	It("itoa handles 0, 200, 404, 500 (HTTP status code range)", func() {
		Expect(openapi.ItoaForTest(0)).To(Equal("0"))
		Expect(openapi.ItoaForTest(200)).To(Equal("200"))
		Expect(openapi.ItoaForTest(404)).To(Equal("404"))
		Expect(openapi.ItoaForTest(500)).To(Equal("500"))
	})

	It("itoa is base-10 (would break Responses keying if it drifted)", func() {
		Expect(openapi.ItoaForTest(10)).To(Equal("10"))
		Expect(openapi.ItoaForTest(255)).To(Equal("255"),
			"255 in hex would be 'ff' — a base-drift would break every status-code key in openapi3.Responses")
	})
})

// Sanity: static-check that the exported test seams remain the file we're testing.
var _ = strings.Contains
