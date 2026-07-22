package testutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/openapi"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/getkin/kin-openapi/openapi3"
)

// TestOpenAPISpecEndpoint asserts /api/openapi.json serves a valid OpenAPI 3
// document. Since testutil.Harness.Router() intentionally registers only a
// subset of routes (see the comment there), the OpenAPI endpoint isn't
// registered on the harness router — we drive Spec() directly, which is what
// the endpoint returns anyway.
func TestOpenAPISpecEndpoint(t *testing.T) {
	_, raw, err := openapi.Spec()
	if err != nil {
		t.Fatalf("Spec(): %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("Spec() returned empty JSON")
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(raw)
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if doc.OpenAPI == "" {
		t.Fatal("parsed doc has no openapi version")
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Fatalf("openapi version %q is not 3.x", doc.OpenAPI)
	}
}

// TestOpenAPIDocsHandlerServesSwaggerUI drives the UI handler and asserts the
// vendored Swagger UI's HTML is served (contains identifiable markers) plus
// our custom initializer swaps in the correct spec URL.
func TestOpenAPIDocsHandlerServesSwaggerUI(t *testing.T) {
	h := openapi.UIHandler("/api/docs")

	// Root → index.html
	{
		req := httptest.NewRequest("GET", "/api/docs/", nil)
		rec := recordFor(h, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/docs/: status %d body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "swagger-ui") {
			preview := body
			if len(preview) > 300 {
				preview = preview[:300]
			}
			t.Errorf("body missing 'swagger-ui' marker:\n%s", preview)
		}
	}

	// swagger-initializer.js → our OWN initializer pointing at /api/openapi.json
	{
		req := httptest.NewRequest("GET", "/api/docs/swagger-initializer.js", nil)
		rec := recordFor(h, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET initializer: status %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"/api/openapi.json"`) {
			t.Errorf("initializer does not point at /api/openapi.json:\n%s", body)
		}
		if strings.Contains(body, "petstore.swagger.io") {
			t.Errorf("initializer still points at the upstream petstore URL — override broken:\n%s", body)
		}
	}

	// A vendored asset (CSS) must serve verbatim.
	{
		req := httptest.NewRequest("GET", "/api/docs/swagger-ui.css", nil)
		rec := recordFor(h, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET swagger-ui.css: status %d", rec.Code)
		}
	}
}

// TestOpenAPIAuthSchemeMatchesHarness is the end-to-end auth check demanded by
// the DJ acceptance criteria: mint a user + token via the harness, hit two
// authenticated endpoints WITH the `Authorization: Basic <token>` header (the
// exact format documented by the spec's bearerAuth scheme), and assert 200.
// Then hit the same endpoints WITHOUT the header and assert the documented
// unauth response.
func TestOpenAPIAuthSchemeMatchesHarness(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("openapi_auth")

	authed := []string{
		"/api/v1/users/current/spaces",   // ListSpaces (registerSpaceRoutes)
		"/api/v1/users/current/curation", // ListCuration (registerCurationRoutes)
		"/api/v1/users/current/widgets/links",
	}
	for _, path := range authed {
		t.Run("with token: "+path, func(t *testing.T) {
			rec := do(t, e, "GET", path, token, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("%s WITH `Authorization: Basic <token>`: status %d body=%s",
					path, rec.Code, rec.Body.String())
			}
		})
		t.Run("no token: "+path, func(t *testing.T) {
			rec := do(t, e, "GET", path, "", nil)
			// Missing Authorization → apierr.MissingAuth() → 400. This matches
			// the spec's ErrBadRequest response.
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s WITHOUT auth header: status %d, want 400", path, rec.Code)
			}
		})
	}
}

// TestOpenAPISpecMatchesAuthShape guards that the security scheme documented
// in the spec matches how resolveUser actually reads the header. If someone
// swaps bearerAuth to type=http scheme=bearer, Swagger UI would emit
// `Authorization: Bearer <token>`, which the real handler rejects. Regressing
// that is a silent break of "Try it out" so we assert the exact shape.
func TestOpenAPISpecMatchesAuthShape(t *testing.T) {
	_, raw, err := openapi.Spec()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var top struct {
		Components struct {
			SecuritySchemes map[string]struct {
				Type string `json:"type"`
				In   string `json:"in"`
				Name string `json:"name"`
			} `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	bearer, ok := top.Components.SecuritySchemes["bearerAuth"]
	if !ok {
		t.Fatal("bearerAuth scheme missing in marshaled spec")
	}
	if bearer.Type != "apiKey" || bearer.In != "header" || bearer.Name != "Authorization" {
		t.Fatalf("bearerAuth wrong shape in JSON: type=%s in=%s name=%s (Swagger UI would prompt for the wrong header)",
			bearer.Type, bearer.In, bearer.Name)
	}
}

// recordFor drives h with req against a fresh recorder.
func recordFor(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
