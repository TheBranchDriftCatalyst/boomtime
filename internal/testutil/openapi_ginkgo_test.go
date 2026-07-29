// openapi_ginkgo_test.go — ginkgo mirror of openapi_test.go (gaka-0vp).
// 1:1 case map (4 stdlib TestXxx; TestOpenAPIAuthSchemeMatchesHarness has 6
// t.Run subtests — 3 paths × {with token, no token} — each becomes a
// separate It):
//   TestOpenAPISpecEndpoint                → OpenAPI Spec > "serves a valid OpenAPI 3 doc"
//   TestOpenAPIDocsHandlerServesSwaggerUI  → OpenAPI Docs > 3 Its (root, initializer, css asset)
//   TestOpenAPIAuthSchemeMatchesHarness    → OpenAPI Auth > 3 paths × 2 Its each (6 total)
//   TestOpenAPISpecMatchesAuthShape        → OpenAPI Spec > "bearerAuth scheme shape matches resolveUser"
package testutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/getkin/kin-openapi/openapi3"
)

// recordForG drives h with req against a fresh recorder (ginkgo-side
// duplicate of recordFor from the stdlib file so both compile side-by-side).
func recordForG(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("OpenAPI Spec", func() {
	It("serves a valid OpenAPI 3 document via Spec()", func() {
		_, raw, err := openapi.Spec()
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).NotTo(BeEmpty())
		loader := openapi3.NewLoader()
		doc, err := loader.LoadFromData(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.OpenAPI).NotTo(BeEmpty())
		Expect(strings.HasPrefix(doc.OpenAPI, "3.")).To(BeTrue(),
			"openapi version %q must be 3.x", doc.OpenAPI)
	})

	It("bearerAuth scheme shape matches how resolveUser reads the header", func() {
		_, raw, err := openapi.Spec()
		Expect(err).NotTo(HaveOccurred())
		var top struct {
			Components struct {
				SecuritySchemes map[string]struct {
					Type string `json:"type"`
					In   string `json:"in"`
					Name string `json:"name"`
				} `json:"securitySchemes"`
			} `json:"components"`
		}
		Expect(json.Unmarshal(raw, &top)).To(Succeed())
		bearer, ok := top.Components.SecuritySchemes["bearerAuth"]
		Expect(ok).To(BeTrue(), "bearerAuth scheme missing in marshaled spec")
		Expect(bearer.Type).To(Equal("apiKey"))
		Expect(bearer.In).To(Equal("header"))
		Expect(bearer.Name).To(Equal("Authorization"))
	})
})

var _ = Describe("OpenAPI Docs (Swagger UI)", func() {
	var h http.Handler
	BeforeEach(func() {
		h = openapi.UIHandler("/api/docs")
	})

	It("serves index.html at the root path", func() {
		req := httptest.NewRequest("GET", "/api/docs/", nil)
		rec := recordForG(h, req)
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("text/html"))
		Expect(rec.Body.String()).To(ContainSubstring("swagger-ui"))
	})

	It("serves our own swagger-initializer.js pointing at /api/openapi.json", func() {
		req := httptest.NewRequest("GET", "/api/docs/swagger-initializer.js", nil)
		rec := recordForG(h, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		body := rec.Body.String()
		Expect(body).To(ContainSubstring(`"/api/openapi.json"`))
		Expect(body).NotTo(ContainSubstring("petstore.swagger.io"),
			"initializer must not still point at the upstream petstore URL")
	})

	It("serves the vendored swagger-ui.css verbatim", func() {
		req := httptest.NewRequest("GET", "/api/docs/swagger-ui.css", nil)
		rec := recordForG(h, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})

var _ = Describe("OpenAPI Auth scheme matches harness", func() {
	// TestOpenAPIAuthSchemeMatchesHarness had 3 paths × 2 sub-cases; ginkgo
	// gets 6 discrete Its via a small paths loop so the test tree mirrors
	// what -v prints for stdlib subtests.
	paths := []string{
		"/api/v1/users/current/spaces",   // ListSpaces
		"/api/v1/users/current/curation", // ListCuration
		"/api/v1/users/current/widgets/links",
	}
	for _, p := range paths {
		p := p
		It("returns 200 WITH `Authorization: Basic <token>` on "+p, func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			_, token := hz.MintUser("openapi_auth")
			rec := doG(e, "GET", p, token, nil)
			Expect(rec.Code).To(Equal(http.StatusOK),
				"%s WITH token: status %d body=%s", p, rec.Code, rec.Body.String())
		})
		It("returns 400 WITHOUT the Authorization header on "+p, func() {
			hz := testutil.NewHarness(GinkgoTB())
			e := hz.Router()
			// no MintUser required — the missing header is the point.
			rec := doG(e, "GET", p, "", nil)
			// Missing Authorization → apierr.MissingAuth() → 400.
			Expect(rec.Code).To(Equal(http.StatusBadRequest),
				"%s WITHOUT auth header: status %d", p, rec.Code)
		})
	}
})
