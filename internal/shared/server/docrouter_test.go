package server

import (
	"sort"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/domainreg"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/handler"
)

func routeKeys(e *echo.Echo) map[string]bool {
	m := map[string]bool{}
	for _, r := range e.Router().Routes() {
		m[r.Method+" "+r.Path] = true
	}
	return m
}

// minDocRoutes is a FLOOR, not an exact count — new routes land often and this
// test must not become a chore. It exists to catch the fixture silently ceasing
// to satisfy a predicate: DocumentationFixture sets compound conditions
// (LiberationEnabled also needs a library path, GithubConnectEnabled also needs
// three OAuth values), and dropping one hides a whole route family with no error
// anywhere. Raise it when the surface grows meaningfully.
const minDocRoutes = 185

// The documentation router must be a strict SUPERSET of what a zero-value
// handler registers. Superset rather than equality because the entire point is
// the extra routes; strictness because a fixture that accidentally disabled
// something would otherwise pass unnoticed.
func TestDocumentationRouterIsStrictSuperset(t *testing.T) {
	bare := echo.New()
	registerRoutes(bare, &handler.Handler{}, domainreg.Build().Registry)
	bareSet := routeKeys(bare)
	docSet := routeKeys(DocumentationRouter())

	var lost []string
	for k := range bareSet {
		if !docSet[k] {
			lost = append(lost, k)
		}
	}
	sort.Strings(lost)
	if len(lost) > 0 {
		t.Errorf("documentation router LOST %d route(s) the bare router has: %v", len(lost), lost)
	}
	if len(docSet) <= len(bareSet) {
		t.Fatalf("documentation router has %d routes vs bare %d — the fixture is not enabling anything",
			len(docSet), len(bareSet))
	}
	if len(docSet) < minDocRoutes {
		t.Errorf("documentation router has %d routes, want >= %d — a feature predicate is probably unsatisfied; "+
			"check the COMPOUND ones in config.DocumentationFixture", len(docSet), minDocRoutes)
	}
	t.Logf("bare=%d doc=%d (+%d)", len(bareSet), len(docSet), len(docSet)-len(bareSet))
}

// One representative route per gated family. A count floor alone would pass if
// one family vanished while another grew, and these are exactly the families
// that were invisible for months.
func TestDocumentationRouterCoversEveryGatedFamily(t *testing.T) {
	docSet := routeKeys(DocumentationRouter())

	for _, tc := range []struct{ family, route string }{
		{"books (BooksEnabled)", "GET /api/v1/books/items"},
		{"liberation (LiberationEnabled — compound, needs BooksLibraryPath)", "GET /api/v1/books/liberation/status"},
		{"github connect (GithubConnectEnabled — compound, needs 3 OAuth values)", "GET /auth/github/connect"},
		{"notifications (gated on BooksEnabled)", "GET /api/v1/notifications"},
		{"admin CLI (FeatureAdminCLI)", "POST /api/v1/admin/cli/run"},
		{"admin jobs (gated on DB availability, not a flag)", "GET /api/v1/admin/jobs"},
		{"admin metrics (gated on DB availability)", "GET /api/v1/admin/metrics"},
		{"books admin (reading monitor)", "GET /api/v1/admin/books/reading-monitor"},
		{"kindle", "POST /api/v1/kindle/sync"},
		{"hardcover", "POST /api/v1/hardcover/pull"},
	} {
		if !docSet[tc.route] {
			t.Errorf("%s: %q missing from the documentation router — that family is invisible to the spec", tc.family, tc.route)
		}
	}
}
