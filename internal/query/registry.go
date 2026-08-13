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
