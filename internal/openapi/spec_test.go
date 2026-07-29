// spec_ginkgo_test.go — ginkgo mirror of spec_test.go (gaka-0vp).
// 1:1 case map (4 stdlib TestXxx):
//
//	TestSpecBuildsAndValidates            → Spec > "builds + validates + round-trips"
//	TestSpecHasSecuritySchemes            → Spec > "has bearerAuth + refreshCookie schemes"
//	TestSpecPublicEndpointsHaveEmptySecurity → Spec > "public endpoints table"
//	TestSpecJSONIsSelfContained           → Spec > "JSON has no external refs"
package openapi_test

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
	"github.com/getkin/kin-openapi/openapi3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("openapi.Spec", func() {
	It("builds, validates, and round-trips through JSON", func() {
		doc, raw, err := openapi.Spec()
		Expect(err).NotTo(HaveOccurred())
		Expect(doc).NotTo(BeNil())
		Expect(raw).NotTo(BeEmpty())
		Expect(doc.Validate(context.Background())).To(Succeed())

		// Round-trip.
		loader := openapi3.NewLoader()
		loaded, err := loader.LoadFromData(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.Validate(loader.Context)).To(Succeed())
	})

	It("has bearerAuth (apiKey/header/Authorization) + refreshCookie schemes", func() {
		doc, _, err := openapi.Spec()
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Components).NotTo(BeNil())
		Expect(doc.Components.SecuritySchemes).NotTo(BeNil())

		bearer := doc.Components.SecuritySchemes["bearerAuth"]
		Expect(bearer).NotTo(BeNil())
		Expect(bearer.Value).NotTo(BeNil())
		// If someone swaps to http/scheme=bearer, Swagger UI prepends a
		// second "Bearer " and breaks resolveUser.
		Expect(bearer.Value.Type).To(Equal("apiKey"))
		Expect(bearer.Value.In).To(Equal("header"))
		Expect(bearer.Value.Name).To(Equal("Authorization"))

		Expect(doc.Components.SecuritySchemes["refreshCookie"]).NotTo(BeNil())
	})

	// Table for the public endpoints — each (path, method) MUST be
	// reachable without auth (Security = empty override).
	pubs := [][2]string{
		{"/auth/login", "POST"},
		{"/auth/register", "POST"},
		{"/badge/svg/{svg}", "GET"},
		{"/widget/svg/{uuid}/{kind}", "GET"},
		{"/api/openapi.json", "GET"},
		{"/api/docs", "GET"},
		{"/api/v1/version", "GET"},
		{"/api/v1/changelog", "GET"},
	}
	Describe("public endpoints override to empty Security", func() {
		for _, p := range pubs {
			p := p
			It(p[1]+" "+p[0], func() {
				doc, _, err := openapi.Spec()
				Expect(err).NotTo(HaveOccurred())

				item := doc.Paths.Find(p[0])
				Expect(item).NotTo(BeNil(), "path missing: %s", p[0])
				op := item.GetOperation(p[1])
				Expect(op).NotTo(BeNil(), "op missing: %s %s", p[1], p[0])
				Expect(op.Security).NotTo(BeNil(),
					"%s %s: no Security override → inherits default bearerAuth", p[1], p[0])
				Expect(*op.Security).To(BeEmpty(),
					"%s %s: Security should be an empty requirements list", p[1], p[0])
			})
		}
	})

	It("emits JSON with no external $refs or CDN URLs", func() {
		_, raw, err := openapi.Spec()
		Expect(err).NotTo(HaveOccurred())

		s := string(raw)
		for _, bad := range []string{
			"petstore.swagger.io", "unpkg.com",
			"cdn.jsdelivr.net", "cdnjs.cloudflare.com",
		} {
			Expect(s).NotTo(ContainSubstring(bad), "forbidden external ref: %s", bad)
		}

		// Walk every $ref and ensure it's #/... (local).
		var v any
		Expect(json.Unmarshal(raw, &v)).To(Succeed())
		var walk func(any)
		walk = func(x any) {
			switch t := x.(type) {
			case map[string]any:
				for k, val := range t {
					if k == "$ref" {
						str, ok := val.(string)
						Expect(ok).To(BeTrue())
						Expect(strings.HasPrefix(str, "#/")).To(BeTrue(),
							"external $ref: %s", str)
					}
					walk(val)
				}
			case []any:
				for _, val := range t {
					walk(val)
				}
			}
		}
		walk(v)
	})
})
