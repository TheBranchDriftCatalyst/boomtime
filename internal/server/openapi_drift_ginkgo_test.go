// openapi_drift_ginkgo_test.go — ginkgo mirror of openapi_drift_test.go
// (gaka-0vp).
//
// 1:1 case map (1 stdlib TestXxx):
//   TestOpenAPISpecCoversEveryRegisteredRoute → OpenAPI drift guard >
//     "every registered route is documented AND every doc entry is a real route"
//
// The single It performs both directions of the drift check (router→spec and
// spec→router) exactly like the stdlib version. Helper functions
// (newRouterForDrift, echoPathToOpenAPI) are shared with the stdlib file
// because both live in the same package.
package server

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OpenAPI drift guard (gaka-lfc)", func() {
	It("router and spec agree on every (method, path) pair", func() {
		e := newRouterForDrift()

		// Collect production routes as (method, openapiPath).
		type routeKey struct{ method, path string }
		got := map[routeKey]struct{}{}
		for _, r := range e.Router().Routes() {
			p := echoPathToOpenAPI(r.Path)
			// Skip the SPA catch-all and the Swagger UI static tree.
			if p == "/*" {
				continue
			}
			if p == "/api/docs/*" {
				p = "/api/docs"
			}
			got[routeKey{method: r.Method, path: p}] = struct{}{}
		}

		// Collect spec paths.
		doc, _, err := openapi.Spec()
		Expect(err).NotTo(HaveOccurred(), "openapi.Spec build failed")

		want := map[routeKey]struct{}{}
		for _, path := range doc.Paths.InMatchingOrder() {
			item := doc.Paths.Value(path)
			if item == nil {
				continue
			}
			for method := range item.Operations() {
				want[routeKey{method: method, path: path}] = struct{}{}
			}
		}

		// Router-registered but not in spec → documentation drift.
		for k := range got {
			_, ok := want[k]
			Expect(ok).To(BeTrue(),
				"router has %s %s but the OpenAPI spec does not (gaka-lfc drift guard: add a doc.AddOperation entry in internal/openapi/spec.go)",
				k.method, k.path)
		}
		// Spec advertises something the router doesn't → dead docs.
		for k := range want {
			_, ok := got[k]
			Expect(ok).To(BeTrue(),
				"OpenAPI spec has %s %s but the router does not register it (remove the stale doc.AddOperation entry, or wire the route)",
				k.method, k.path)
		}
	})
})
