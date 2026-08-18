// Package query is boomtime's tight, typed cross-domain query DSL (gaka-174.q).
//
// One grammar — from(domain)·where·group·measure·over·bucket·having·sort·limit —
// compiles to owner-scoped SQL over ANY registered domain (coding, reading, …)
// and returns a typed Result. It is the shared engine dashboards, goals, and
// canonical bucketing consume.
//
// SAFETY MODEL (reused verbatim from internal/db/heartbeats_explore.go and
// internal/goals/eval.go): a dimension/measure NAME is the ONLY thing that ever
// becomes SQL, and only after passing the domain registry whitelist — the raw
// name is never interpolated. Every user VALUE travels as a query arg
// ($1,$2,…), never string-concatenated. A measure is bound to exactly ONE
// table; a group/where dimension is valid only if it lives on that same table
// (measure.Dims). There are NO cross-table joins in v1.
package query

// Domain is a queryable data domain (e.g. "coding", "reading"). It owns the
// whitelist of measures and dimensions the grammar may reference.
type Domain struct {
	Name       string
	Measures   map[string]Measure
	Dimensions map[string]Dimension

	// Rows, when set, is the source for leaf-rows mode (Query.Rows()): the entity
	// table this domain lists row-by-row under the same owner scope + range +
	// where predicate as its aggregates. nil = the domain has no row listing.
	Rows *RowSource
}

// RowSource describes a domain's leaf-rows projection: which table to list, its
// owner/date columns (TRUSTED, mirroring Measure), the projected columns, and a
// default ordering. It carries NO aggregate — it is the "show me the actual
// rows under this drill path" surface.
type RowSource struct {
	Table       string      // trusted table name
	OwnerCol    string      // trusted owner-scoping column
	DateCol     string      // trusted time-bucketing column (for range filtering)
	Columns     []RowColumn // projected columns, in output order
	DefaultSort string      // trusted ORDER BY clause (e.g. "finished_at DESC NULLS LAST")
}

// RowColumn is one projected leaf-row column. Name is the output key (the JSON
// key the FE reads — quoted verbatim as the SQL alias so Postgres never
// case-folds it); Expr is the TRUSTED source SQL column/expression. Both are
// author-supplied, never user input.
type RowColumn struct {
	Name string
	Expr string
}

// Measure is an aggregate bound to ONE table. Expr/DateCol/OwnerCol are TRUSTED
// SQL (author-supplied, never user input). Dims is the whitelist of dimension
// names that may group/filter WITH this measure — necessarily same-table, since
// v1 forbids joins. A grouped listening-seconds query, for instance, only
// supports source/date because reading_activity has no per-book attribution.
type Measure struct {
	Name     string
	Table    string
	Expr     string   // trusted SQL agg: "sum(total_seconds)", "count(*)", …
	DateCol  string   // trusted time-bucketing column on Table
	OwnerCol string   // trusted owner-scoping column ("sender" | "owner")
	Dims     []string // dimension names valid WITH this measure (same table)
}

// supportsDim reports whether a dimension name is in this measure's whitelist.
func (m Measure) supportsDim(name string) bool {
	for _, d := range m.Dims {
		if d == name {
			return true
		}
	}
	return false
}

// Dimension is a group/filter axis. Expr is TRUSTED SQL — usually a bare column
// ("project"), but may be a jsonb extract ("genres->>0"). Table pins it to a
// single table so the compiler can reject cross-table references.
type Dimension struct {
	Name  string
	Table string
	Expr  string // trusted SQL
}

// registry holds every registered domain, keyed by name.
var registry = map[string]Domain{}

// Register adds a domain to the global registry. Called from domains.go init.
// Panics on a duplicate name — a registration collision is a programming error,
// not a runtime condition.
func Register(d Domain) {
	if _, dup := registry[d.Name]; dup {
		panic("query: duplicate domain registration: " + d.Name)
	}
	registry[d.Name] = d
}

// lookupDomain returns a registered domain and whether it exists.
func lookupDomain(name string) (Domain, bool) {
	d, ok := registry[name]
	return d, ok
}

// rowsMeasure builds the synthetic Measure the leaf-rows path hands to
// buildPredicate, so a rows where-filter is validated by the SAME same-table
// whitelist the aggregate path uses. Its Dims are the union of every measure's
// Dims that share the RowSource table — i.e. exactly the dimensions an author
// already vetted as valid on that table (Dimension.Table is descriptive only,
// so it is NOT used for this). ok=false when the domain has no RowSource.
func rowsMeasure(dom Domain) (Measure, bool) {
	rs := dom.Rows
	if rs == nil {
		return Measure{}, false
	}
	seen := map[string]bool{}
	var dims []string
	for _, m := range dom.Measures {
		if m.Table != rs.Table {
			continue
		}
		for _, dn := range m.Dims {
			if !seen[dn] {
				seen[dn] = true
				dims = append(dims, dn)
			}
		}
	}
	return Measure{Name: "__rows", Table: rs.Table, DateCol: rs.DateCol, OwnerCol: rs.OwnerCol, Dims: dims}, true
}
