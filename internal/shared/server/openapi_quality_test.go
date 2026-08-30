package server

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/openapi"
)

// A RATCHET, not a snapshot. These ceilings may only ever go DOWN.
//
// Documentation rots by accretion: one undocumented route is invisible, thirty
// are a spec nobody trusts. The spec reached 192 operations with 50 placeholder
// schemas, 91 missing-or-auto descriptions and 75 auto summaries precisely
// because nothing failed when a route arrived undocumented — the auto-derive
// pass made every new route *appear* covered while documenting nothing.
//
// So: adding a route without documenting it fails here. If you are looking at a
// failure telling you to raise one of these, the answer is to document the route
// instead. When you legitimately drive one lower, lower the constant too — that
// is the ratchet clicking.
const (
	maxPlaceholderSchemas = 0
	maxWeakDescriptions   = 0
	maxAutoSummaries      = 0
	maxNoSuccessResponse  = 0 // 1xx/2xx/3xx all count as a documented success
)

// autoSummaryRe matches the summary the auto-derive pass generates:
// a capitalised method name followed by the route path.
var autoSummaryRe = regexp.MustCompile(`^(Get|Post|Put|Patch|Delete|Head|Options) /`)

type specDefects struct {
	placeholderSchema []string
	weakDescription   []string
	autoSummary       []string
	noSuccess         []string
	total             int
}

// hasRealSchema reports whether a media type carries something a client could
// actually code against, rather than a bare object placeholder.
func hasRealSchema(mt *openapi3.MediaType) bool {
	if mt == nil || mt.Schema == nil {
		return false
	}
	if mt.Schema.Ref != "" {
		return true // a $ref to a named component
	}
	v := mt.Schema.Value
	if v == nil {
		return false
	}
	// Properties, array items, oneOf/anyOf, or a non-object primitive all count.
	// A bare {"type":"object"} does not — that is the placeholder.
	if len(v.Properties) > 0 || v.Items != nil || len(v.OneOf) > 0 || len(v.AnyOf) > 0 {
		return true
	}
	if v.Type != nil && !v.Type.Is("object") {
		return true
	}
	return v.AdditionalProperties.Has != nil || v.AdditionalProperties.Schema != nil
}

func auditSpec(t *testing.T) specDefects {
	t.Helper()
	openapi.SetDocumentationRouter(DocumentationRouter())
	doc, _, err := openapi.Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}

	var d specDefects
	for _, p := range doc.Paths.InMatchingOrder() {
		for method, op := range doc.Paths.Value(p).Operations() {
			d.total++
			id := method + " " + p

			if op.Description == "" || strings.HasPrefix(op.Description, "Auto-derived stub") {
				d.weakDescription = append(d.weakDescription, id)
			}
			// The derived summary is exactly "<Method> <path>", e.g.
			// "Get /api/v1/books/items". Matching on any slash was too crude and
			// flagged real prose like "Pause / resume a goal" — a summary may
			// contain a slash, it just must not BE a method plus a path.
			if op.Summary == "" || autoSummaryRe.MatchString(op.Summary) {
				d.autoSummary = append(d.autoSummary, id)
			}

			found, documented := false, false
			for code, r := range op.Responses.Map() {
				// A documented SUCCESS is not necessarily 2xx. A WebSocket
				// handshake succeeds with 101 and an OAuth entry point succeeds
				// with a 3xx redirect; demanding 2xx would report correctly
				// documented routes as broken, which is what this check did on
				// its first run.
				if len(code) == 0 || r.Value == nil {
					continue
				}
				if code[0] != '1' && code[0] != '2' && code[0] != '3' {
					continue
				}
				found = true
				// 204, 101 and 3xx carry no body by definition; "no content" IS
				// the complete answer for them.
				if len(r.Value.Content) == 0 {
					documented = true
					continue
				}
				for _, mt := range r.Value.Content {
					if hasRealSchema(mt) {
						documented = true
					}
				}
			}
			switch {
			case !found:
				d.noSuccess = append(d.noSuccess, id)
			case !documented:
				d.placeholderSchema = append(d.placeholderSchema, id)
			}
		}
	}
	for _, s := range [][]string{d.placeholderSchema, d.weakDescription, d.autoSummary, d.noSuccess} {
		sort.Strings(s)
	}
	return d
}

func report(t *testing.T, label string, got []string, ceiling int) {
	t.Helper()
	if len(got) <= ceiling {
		return
	}
	t.Errorf("%s: %d operations, ceiling %d. These are undocumented:", label, len(got), ceiling)
	for i, id := range got {
		if i >= 25 {
			t.Errorf("    ... and %d more", len(got)-25)
			break
		}
		t.Errorf("    %s", id)
	}
}

// Every operation must describe a success response a client can code against.
func TestSpecHasNoPlaceholderSchemas(t *testing.T) {
	d := auditSpec(t)
	t.Logf("%d operations audited", d.total)
	report(t, "placeholder 2xx schema", d.placeholderSchema, maxPlaceholderSchemas)
	report(t, "no 2xx response at all", d.noSuccess, maxNoSuccessResponse)
}

// Prose is documentation too. A summary that restates the path and a description
// that says "auto-derived stub" tell a reader nothing they did not already know
// from the URL.
func TestSpecHasNoGenericProse(t *testing.T) {
	d := auditSpec(t)
	report(t, "missing or auto-generated description", d.weakDescription, maxWeakDescriptions)
	report(t, "auto-generated summary", d.autoSummary, maxAutoSummaries)
}
