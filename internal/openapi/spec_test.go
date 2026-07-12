package openapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
	"github.com/getkin/kin-openapi/openapi3"
)

// TestSpecBuildsAndValidates asserts the whole spec constructs, validates
// against the OpenAPI 3 rules, and round-trips through JSON. This is the
// canary that catches any typo in the schema builder (missing description,
// dangling $ref, unknown status code, etc.).
func TestSpecBuildsAndValidates(t *testing.T) {
	doc, raw, err := openapi.Spec()
	if err != nil {
		t.Fatalf("Spec() build error: %v", err)
	}
	if doc == nil {
		t.Fatal("Spec() returned nil doc")
	}
	if len(raw) == 0 {
		t.Fatal("Spec() returned empty JSON")
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("built spec fails openapi3 validation: %v", err)
	}
	// Round-trip: re-parse the JSON and re-validate. This guards against
	// marshaling drift (e.g. an inline schema that serializes to invalid JSON).
	loader := openapi3.NewLoader()
	loaded, err := loader.LoadFromData(raw)
	if err != nil {
		t.Fatalf("round-trip: LoadFromData failed: %v", err)
	}
	if err := loaded.Validate(loader.Context); err != nil {
		t.Fatalf("round-trip: reloaded spec fails validation: %v", err)
	}
}

// TestSpecHasSecuritySchemes ensures both the bearerAuth apiKey scheme and the
// refreshCookie scheme are declared — Swagger UI's Authorize button depends on
// these to render input for the user's token.
func TestSpecHasSecuritySchemes(t *testing.T) {
	doc, _, err := openapi.Spec()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if doc.Components == nil || doc.Components.SecuritySchemes == nil {
		t.Fatal("no components.securitySchemes")
	}
	bearer := doc.Components.SecuritySchemes["bearerAuth"]
	if bearer == nil || bearer.Value == nil {
		t.Fatal("bearerAuth scheme missing")
	}
	// Guard the exact shape: apiKey / in=header / name=Authorization. If
	// someone swaps to type=http scheme=bearer, Swagger UI would prepend a
	// second "Bearer " and break resolveUser (which expects "Basic <token>").
	if bearer.Value.Type != "apiKey" || bearer.Value.In != "header" || bearer.Value.Name != "Authorization" {
		t.Fatalf("bearerAuth wrong shape: type=%s in=%s name=%s",
			bearer.Value.Type, bearer.Value.In, bearer.Value.Name)
	}
	if doc.Components.SecuritySchemes["refreshCookie"] == nil {
		t.Fatal("refreshCookie scheme missing")
	}
}

// TestSpecPublicEndpointsHaveEmptySecurity asserts that endpoints reachable
// without auth explicitly declare empty security (overriding the default
// bearerAuth), so Swagger UI marks them as auth-less.
func TestSpecPublicEndpointsHaveEmptySecurity(t *testing.T) {
	doc, _, err := openapi.Spec()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// (path, method) pairs that MUST be public.
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
	for _, p := range pubs {
		item := doc.Paths.Find(p[0])
		if item == nil {
			t.Errorf("public path missing from spec: %s", p[0])
			continue
		}
		op := item.GetOperation(p[1])
		if op == nil {
			t.Errorf("public op missing: %s %s", p[1], p[0])
			continue
		}
		if op.Security == nil {
			t.Errorf("%s %s: no Security override → inherits default bearerAuth; expected explicit empty security", p[1], p[0])
			continue
		}
		if len(*op.Security) != 0 {
			t.Errorf("%s %s: Security should be an empty requirements list", p[1], p[0])
		}
	}
}

// TestSpecJSONIsSelfContained asserts the emitted JSON has no external $refs
// or CDN URLs (the "no CDN" constraint). Every $ref must be a local #/... ref
// and the document must not link to petstore or unpkg.
func TestSpecJSONIsSelfContained(t *testing.T) {
	_, raw, err := openapi.Spec()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(raw)
	forbidden := []string{
		"petstore.swagger.io",
		"unpkg.com",
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
	}
	for _, bad := range forbidden {
		if strings.Contains(s, bad) {
			t.Errorf("spec JSON contains forbidden external ref/URL: %q", bad)
		}
	}
	// Walk every $ref and ensure it starts with '#/'.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("re-parse JSON: %v", err)
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if k == "$ref" {
					if str, ok := val.(string); ok && !strings.HasPrefix(str, "#/") {
						// External ref
					}
					if str, ok := val.(string); ok && !strings.HasPrefix(str, "#/") {
						// (double-check block guards against future compilers'
						// smart-branch pruning; keep it obvious)
						panic("external ref: " + str)
					}
				}
				walk(val)
			}
		case []any:
			for _, val := range t {
				walk(val)
			}
		}
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("external $ref found: %v", r)
		}
	}()
	walk(v)
}
