package server

import (
	"regexp"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
	"github.com/labstack/echo/v5"
)

// TestOpenAPISpecCoversEveryRegisteredRoute is the drift guard promised by
// gaka-lfc: every path registered on the production router MUST have a matching
// OpenAPI path entry (with the SAME method). Adds/deletes/moves in the router
// without a corresponding spec update fail this test, so the docs cannot silently
// go stale.
//
// It also asserts the OpenAPI spec doesn't advertise a phantom endpoint — every
// spec path must be a real registered route. That direction guards against
// stale spec entries after a handler is removed.
//
// Some intentional deviations:
//   - The catch-all "/*" SPA route is skipped (it's not an API endpoint).
//   - The Docs endpoints (/api/docs and /api/docs/*) collapse to the single
//     spec path /api/docs (Swagger UI + its static assets share one doc entry).
func TestOpenAPISpecCoversEveryRegisteredRoute(t *testing.T) {
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
			// Collapsed into the single /api/docs spec entry.
			p = "/api/docs"
		}
		got[routeKey{method: r.Method, path: p}] = struct{}{}
	}

	// Collect spec paths.
	doc, _, err := openapi.Spec()
	if err != nil {
		t.Fatalf("openapi.Spec build: %v", err)
	}
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
		if _, ok := want[k]; !ok {
			t.Errorf("router has %s %s but the OpenAPI spec does not (gaka-lfc drift guard: add a doc.AddOperation entry in internal/openapi/spec.go)", k.method, k.path)
		}
	}
	// Spec advertises something the router doesn't → dead docs.
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("OpenAPI spec has %s %s but the router does not register it (remove the stale doc.AddOperation entry, or wire the route)", k.method, k.path)
		}
	}
}

// newRouterForDrift builds an echo router registered EXACTLY like New() does,
// minus middleware and the static SPA server (both irrelevant to route
// enumeration). Handlers are shared by reference to a zero-value Handler; no
// requests are dispatched, so no methods are called.
func newRouterForDrift() *echo.Echo {
	e := echo.New()
	h := &handler.Handler{}
	registerRoutes(e, h)
	// registerStatic adds a "/*" catch-all; we DON'T include it here — the
	// drift check specifically skips it, but building it also requires a
	// working embed which the test binary has (the stub dist file).
	return e
}

// echoPathRe matches Echo's `:name` path parameter syntax.
var echoPathRe = regexp.MustCompile(`:([a-zA-Z_][a-zA-Z0-9_]*)`)

// echoPathToOpenAPI rewrites Echo's `:name` params to OpenAPI's `{name}`.
func echoPathToOpenAPI(p string) string {
	return echoPathRe.ReplaceAllStringFunc(p, func(s string) string {
		// s is ":name" — strip the colon, wrap in braces.
		return "{" + strings.TrimPrefix(s, ":") + "}"
	})
}
