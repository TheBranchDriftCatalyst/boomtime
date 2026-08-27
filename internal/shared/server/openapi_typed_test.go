package server

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/domainreg"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/openapi"
)

// resolve follows a SchemaRef to its value, whether it is inline or a $ref.
func resolve(doc *openapi3.T, ref *openapi3.SchemaRef) *openapi3.Schema {
	if ref == nil {
		return nil
	}
	if ref.Value != nil {
		return ref.Value
	}
	return nil
}

func specForDocs(t *testing.T) *openapi3.T {
	t.Helper()
	openapi.SetDocumentationRouter(DocumentationRouter(domainreg.Build().Registry))
	doc, _, err := openapi.Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	return doc
}

// Routes registered through the typed seam must carry a REAL schema — named
// properties, not the bare {"type":"object"} stub every other route gets.
//
// Asserting on specific property names rather than "has some properties"
// matters: openapi3gen silently yields an empty object for a type it cannot
// reflect, which would still look structurally fine while documenting nothing.
func TestTypedSeamProducesRealSchemas(t *testing.T) {
	doc := specForDocs(t)

	for _, tc := range []struct {
		path  string
		props []string
	}{
		{"/api/v1/books/liberation/status", []string{"counts", "pending", "excluded", "libraryPath"}},
		{"/api/v1/books/liberation/excluded", []string{"items"}},
	} {
		item := doc.Paths.Value(tc.path)
		if item == nil || item.Get == nil {
			t.Errorf("%s: missing from the spec entirely", tc.path)
			continue
		}
		resp := item.Get.Responses.Value("200")
		if resp == nil || resp.Value == nil {
			t.Errorf("%s: no 200 response", tc.path)
			continue
		}
		mt := resp.Value.Content["application/json"]
		if mt == nil {
			t.Errorf("%s: 200 has no application/json content", tc.path)
			continue
		}
		sch := resolve(doc, mt.Schema)
		if sch == nil {
			t.Errorf("%s: 200 schema did not resolve", tc.path)
			continue
		}
		if len(sch.Properties) == 0 {
			t.Errorf("%s: 200 schema is a bare object — the typed seam did not supply a schema", tc.path)
			continue
		}
		for _, want := range tc.props {
			if _, ok := sch.Properties[want]; !ok {
				t.Errorf("%s: 200 schema missing property %q (got %v)", tc.path, want, keysOf(sch.Properties))
			}
		}
	}
}

// The nested type must be reflected too, not flattened to a bare array of
// objects — ExcludedItem's fields are the entire value of that endpoint.
func TestTypedSeamReflectsNestedTypes(t *testing.T) {
	doc := specForDocs(t)
	item := doc.Paths.Value("/api/v1/books/liberation/excluded")
	if item == nil || item.Get == nil {
		t.Fatal("route missing")
	}
	sch := resolve(doc, item.Get.Responses.Value("200").Value.Content["application/json"].Schema)
	items := sch.Properties["items"]
	if items == nil || items.Value == nil {
		t.Fatal("items property missing")
	}
	if items.Value.Type == nil || !items.Value.Type.Is("array") {
		t.Fatalf("items is %v, want array", items.Value.Type)
	}
	elem := resolve(doc, items.Value.Items)
	if elem == nil || len(elem.Properties) == 0 {
		t.Fatal("array element has no properties — the nested struct was not reflected")
	}
	for _, want := range []string{"asin", "title", "status", "attempts", "retryable"} {
		if _, ok := elem.Properties[want]; !ok {
			t.Errorf("ExcludedItem schema missing %q (got %v)", want, keysOf(elem.Properties))
		}
	}
}

func keysOf(m openapi3.Schemas) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
