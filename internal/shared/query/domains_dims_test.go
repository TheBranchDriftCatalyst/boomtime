package query_test

import (
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/query"
)

// domains_dims_test.go — pins the reading domain's series/author/genre grouping
// dimensions (boom-books C). Pure Compile assertions: no pg harness, so these
// run everywhere. Non-tautological — the runtime-by-author case FAILS if author
// is dropped from the runtime measure's Dims whitelist (the gap this closed).

func TestReading_BooksGroupByDims_Compile(t *testing.T) {
	for _, dim := range []string{"series", "author", "genre", "status", "source"} {
		sql, _, err := query.Compile("alice",
			query.Q("reading").Measure("books").Group(dim))
		if err != nil {
			t.Errorf("books group by %q: unexpected error: %v", dim, err)
			continue
		}
		if !strings.Contains(sql, "GROUP BY") {
			t.Errorf("books group by %q: no GROUP BY in %q", dim, sql)
		}
	}
}

func TestReading_RuntimeGroupByDims_Compile(t *testing.T) {
	// author was previously NOT whitelisted on runtime — this asserts the fix.
	for dim, wantExpr := range map[string]string{
		"series": "series",
		"author": "authors",    // reading_items.authors
		"genre":  "genres->>0", // first jsonb element
	} {
		sql, _, err := query.Compile("bob",
			query.Q("reading").Measure("runtime").Group(dim))
		if err != nil {
			t.Errorf("runtime group by %q: unexpected error: %v", dim, err)
			continue
		}
		if !strings.Contains(sql, wantExpr) {
			t.Errorf("runtime group by %q: expected expr %q in SQL, got %q", dim, wantExpr, sql)
		}
	}
}

func TestReading_RuntimeGroupByAuthor_Filter(t *testing.T) {
	// A where-filter on author must also resolve on the runtime measure now.
	sql, args, err := query.Compile("carol",
		query.Q("reading").Measure("runtime").
			Where(query.Leaf("author", query.OpEq, "Jemisin")).
			Group("series"))
	if err != nil {
		t.Fatalf("runtime filter by author: %v", err)
	}
	if !strings.Contains(sql, "authors") {
		t.Errorf("expected authors predicate in SQL, got %q", sql)
	}
	if len(args) < 2 || args[len(args)-1] != "Jemisin" {
		t.Errorf("expected 'Jemisin' as a positional arg, got %v", args)
	}
}
