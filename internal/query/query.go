package query

import "time"

// Granularity is the time-bucketing unit for a series query. "none" means no
// time axis (the result is a scalar or a grouped listing, not a time series).
type Granularity string

const (
	GranNone  Granularity = "none"
	GranDay   Granularity = "day"
	GranWeek  Granularity = "week"
	GranMonth Granularity = "month"
)

// trunc maps a granularity to the Postgres date_trunc field. Only called for the
// three real granularities (none is handled before this).
func (g Granularity) trunc() (string, bool) {
	switch g {
	case GranDay:
		return "day", true
	case GranWeek:
		return "week", true
	case GranMonth:
		return "month", true
	default:
		return "", false
	}
}

// Range bounds a query in time. Two forms:
//   - last-N-units: LastN>0 → the most recent N units of the Over granularity
//     (N days / weeks / months) ending at the anchor "now".
//   - explicit: Start/End set → that half-open-inclusive window.
//
// The zero Range means "no time bound" (lifetime).
type Range struct {
	LastN      int
	Start, End time.Time
}

// LastN builds a last-N-units range (unit = the Over granularity).
func LastN(n int) Range { return Range{LastN: n} }

// Between builds an explicit inclusive [start,end] range.
func Between(start, end time.Time) Range { return Range{Start: start, End: end} }

func (r Range) isZero() bool {
	return r.LastN == 0 && r.Start.IsZero() && r.End.IsZero()
}

// Op is a leaf predicate comparison.
type Op string

const (
	OpEq    Op = "eq"
	OpNeq   Op = "neq"
	OpIn    Op = "in"
	OpILike Op = "ilike" // case-insensitive substring match (SQL ILIKE '%value%')
)

// PredKind tags a predicate node.
type PredKind string

const (
	PredLeaf PredKind = "leaf"
	PredAnd  PredKind = "and"
	PredOr   PredKind = "or"
	PredNot  PredKind = "not"
)

// Predicate is one node of the where-tree: a leaf {dimension, op, values} or a
// boolean combinator over children. The leaf dimension is resolved against the
// domain registry at compile time (whitelist → trusted column); the values ride
// as query args.
type Predicate struct {
	Kind PredKind

	// leaf
	Dimension string
	Op        Op
	Values    []string

	// and / or / not
	Of []*Predicate
}

// Leaf builds a leaf predicate: dimension OP values.
func Leaf(dimension string, op Op, values ...string) *Predicate {
	return &Predicate{Kind: PredLeaf, Dimension: dimension, Op: op, Values: values}
}

// And / Or / Not build combinator nodes.
func And(of ...*Predicate) *Predicate { return &Predicate{Kind: PredAnd, Of: of} }
func Or(of ...*Predicate) *Predicate  { return &Predicate{Kind: PredOr, Of: of} }
func Not(p *Predicate) *Predicate     { return &Predicate{Kind: PredNot, Of: []*Predicate{p}} }

// BucketPolicy collapses a grouped result to keep only the pinned + top-N rows,
// rolling everything else into a single "Other" row. Pinned canonical values are
// NEVER bucketed away regardless of rank. (The canonical-entities feature will
// feed Pin later; for now it is just a passed-in slice.)
type BucketPolicy struct {
	TopN  int      // keep the top-N non-pinned rows by measure (<=0 = keep all)
	Pin   []string // canonical values always kept (case-insensitive)
	Other bool     // roll the remainder into an "Other" row
}

// HavingCond filters grouped/series buckets by their measure value.
type HavingCond struct {
	Op    string // ">=", "<=", ">", "<", "==", "!="
	Value float64
}

// Query is an immutable-ish fluent builder. Builders mutate in place and return
// the receiver so calls chain; validation is deferred to Compile.
type Query struct {
	domain  string
	measure string
	group   string
	where   *Predicate
	gran    Granularity
	rng     Range
	bucket  *BucketPolicy
	having  *HavingCond

	sortField string
	sortDesc  bool
	sortSet   bool

	// rollups requests extra per-group measures computed in the SAME grouped
	// query; each lands in Group.Stats (with an always-present "count").
	rollups []string

	// rowsMode switches the query to leaf-rows mode (Domain.Rows), paginated by
	// page/pageSize instead of aggregating.
	rowsMode bool
	page     int
	pageSize int

	limit int
	now   time.Time
}

// Q starts a query against a domain (the from(domain) verb).
func Q(domain string) *Query { return &Query{domain: domain, gran: GranNone} }

// Measure selects the aggregate.
func (q *Query) Measure(name string) *Query { q.measure = name; return q }

// Where sets the filter predicate tree.
func (q *Query) Where(p *Predicate) *Query { q.where = p; return q }

// Group sets the group-by dimension (mutually exclusive with a non-none Over).
func (q *Query) Group(dim string) *Query { q.group = dim; return q }

// Over sets the time granularity + range (the over(window) verb).
func (q *Query) Over(gran Granularity, r Range) *Query { q.gran = gran; q.rng = r; return q }

// Bucket sets the top-N/pin/Other roll-up policy (applied to a grouped result).
func (q *Query) Bucket(p BucketPolicy) *Query { q.bucket = &p; return q }

// Having sets the post-aggregation measure filter.
func (q *Query) Having(c HavingCond) *Query { q.having = &c; return q }

// Sort sets an explicit ordering. field is "measure", "bucket", or the group
// dimension name.
func (q *Query) Sort(field string, desc bool) *Query {
	q.sortField, q.sortDesc, q.sortSet = field, desc, true
	return q
}

// Rollups requests additional per-group measures computed in the SAME grouped
// query (one round-trip). Each name must be another measure on the same table
// as the grouping measure; results land in each Group.Stats alongside an
// always-present "count". Ignored by a non-grouped query.
func (q *Query) Rollups(names ...string) *Query { q.rollups = names; return q }

// Rows switches the query into leaf-rows mode: instead of an aggregate it
// returns the domain's entity rows (Domain.Rows) under the SAME owner scope +
// range + where predicate as the aggregate path, paginated by Page.
func (q *Query) Rows() *Query { q.rowsMode = true; return q }

// Page sets the 1-based page number + page size for rows mode. Non-positive
// values fall back to page 1 / a default size at compile time.
func (q *Query) Page(number, size int) *Query { q.page, q.pageSize = number, size; return q }

// Limit caps the number of returned rows.
func (q *Query) Limit(n int) *Query { q.limit = n; return q }

// At pins the anchor time for last-N range resolution (defaults to now()).
// Exposed mainly for deterministic tests.
func (q *Query) At(t time.Time) *Query { q.now = t; return q }

// ResultKind tags the Result union.
type ResultKind string

const (
	ResultScalar ResultKind = "scalar"
	ResultSeries ResultKind = "series"
	ResultGroups ResultKind = "groups"
	ResultRows   ResultKind = "rows"
)

// Point is one time-series bucket.
type Point struct {
	Bucket time.Time
	Value  float64
}

// Group is one grouped-listing row (already bucket-policy-applied when a policy
// was set).
type Group struct {
	Key   string
	Value float64

	// Stats carries the per-group multi-measure rollups when the query set
	// Rollups(...): always an entry "count", plus one per requested rollup
	// measure. nil on the single-measure back-compat path. The primary measure
	// stays in Value (Stats is additive, never a replacement).
	Stats map[string]float64
}

// Result is the typed union the consumers read. Exactly one arm is populated,
// selected by Kind.
type Result struct {
	Kind   ResultKind
	Scalar float64
	Series []Point
	Groups []Group

	// Rows/Total are populated only for ResultRows (leaf-rows mode): Rows is the
	// owner-scoped page of entity rows keyed by the RowSource column names; Total
	// is the unpaginated row count for the same filter.
	Rows  []map[string]any
	Total int
}
