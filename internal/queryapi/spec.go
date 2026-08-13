// Package queryapi is the thin HTTP surface over the internal/query DSL
// (gaka-174.q). It exposes ONE owner-scoped endpoint — POST /api/v1/query —
// that maps a typed JSON spec onto a *query.Query, validates it against the
// domain registry (Compile), runs it, and returns the typed Result as JSON.
//
// SAFETY: this package builds NO SQL. Every dimension/measure/domain name in
// the spec is handed to query.Compile, which whitelists it against the domain
// registry before it can become SQL (unknown → error → 400). Every user VALUE
// rides as a positional query arg inside the DSL. The endpoint therefore
// inherits the DSL's injection-proof guarantee verbatim.
package queryapi

import (
	"fmt"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/query"
)

// Spec is the JSON request body. It mirrors the DSL grammar
// (from(domain)·where·group·measure·over·bucket·having·sort·limit) one field
// per verb. Only domain + measure are required; everything else is optional and
// defaults to the DSL's zero behavior (lifetime scalar, no filter, no sort).
type Spec struct {
	Domain  string         `json:"domain"`
	Measure string         `json:"measure"`
	Where   *PredicateNode `json:"where,omitempty"`
	Group   string         `json:"group,omitempty"`
	Over    *OverSpec      `json:"over,omitempty"`
	Bucket  *BucketSpec    `json:"bucket,omitempty"`
	Having  *HavingSpec    `json:"having,omitempty"`
	Sort    *SortSpec      `json:"sort,omitempty"`
	Limit   int            `json:"limit,omitempty"`

	// Rollups requests extra per-group measures alongside the grouped measure;
	// each lands in the group's `stats` (plus an always-present `count`).
	Rollups []string `json:"rollups,omitempty"`

	// Rows switches to leaf-rows mode (no aggregate): the entity rows under the
	// where predicate, owner-scoped + paginated by Page. Returns a `rows` result.
	Rows bool      `json:"rows,omitempty"`
	Page *PageSpec `json:"page,omitempty"`
}

// PageSpec is the 1-based pagination window for leaf-rows mode.
type PageSpec struct {
	Number int `json:"number"`
	Size   int `json:"size"`
}

// PredicateNode mirrors query.Predicate: a leaf {dim, op, values} or a boolean
// combinator (and/or/not) over child nodes. Unknown kinds/ops are NOT rejected
// here — they flow into Compile, which owns the whitelist and turns them into a
// 400. This keeps the mapping a dumb structural transform.
type PredicateNode struct {
	Kind   string           `json:"kind"`             // leaf | and | or | not
	Dim    string           `json:"dim,omitempty"`    // leaf: dimension name
	Op     string           `json:"op,omitempty"`     // leaf: eq | neq | in
	Values []string         `json:"values,omitempty"` // leaf: comparison values
	Of     []*PredicateNode `json:"of,omitempty"`     // and/or/not children
}

// OverSpec carries the time granularity + range (the over(window) verb).
// Granularity defaults to "none" (a scalar / grouped listing, no time axis).
type OverSpec struct {
	Granularity string     `json:"granularity,omitempty"` // none | day | week | month
	Range       *RangeSpec `json:"range,omitempty"`
}

// RangeSpec bounds the query in time. Exactly one of lastN / between may be set;
// both set is a 400. Neither set (or a nil OverSpec.Range) means "lifetime". The
// unit of lastN is the granularity above (days when granularity is none) — the
// DSL derives it from the Over granularity, so there is deliberately no separate
// unit field to contradict it.
type RangeSpec struct {
	LastN   *int         `json:"lastN,omitempty"`
	Between *BetweenSpec `json:"between,omitempty"`
}

// BetweenSpec is an explicit inclusive [start, end] window.
type BetweenSpec struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// BucketSpec is the top-N/pin/Other roll-up policy (grouped queries only).
type BucketSpec struct {
	TopN  int      `json:"topN"`
	Pin   []string `json:"pin,omitempty"`
	Other bool     `json:"other,omitempty"`
}

// HavingSpec is the post-aggregation measure filter.
type HavingSpec struct {
	Op    string  `json:"op"` // >= <= > < == !=
	Value float64 `json:"value"`
}

// SortSpec sets an explicit ordering. Field is "measure"/"value", "bucket", or
// the group dimension name / "key".
type SortSpec struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// toQuery maps the spec onto a *query.Query. It performs only structural
// validation the DSL builder cannot express (mutually-exclusive range arms);
// all NAME/whitelist validation is deferred to query.Compile so there is a
// single source of truth for what a domain supports.
func (s *Spec) toQuery() (*query.Query, error) {
	q := query.Q(s.Domain).Measure(s.Measure)

	if s.Where != nil {
		q.Where(s.Where.toPredicate())
	}
	if s.Group != "" {
		q.Group(s.Group)
	}
	if s.Over != nil {
		gran := query.Granularity(s.Over.Granularity)
		if s.Over.Granularity == "" {
			gran = query.GranNone
		}
		r, err := s.Over.Range.toRange()
		if err != nil {
			return nil, err
		}
		q.Over(gran, r)
	}
	if s.Bucket != nil {
		q.Bucket(query.BucketPolicy{
			TopN:  s.Bucket.TopN,
			Pin:   s.Bucket.Pin,
			Other: s.Bucket.Other,
		})
	}
	if s.Having != nil {
		q.Having(query.HavingCond{Op: s.Having.Op, Value: s.Having.Value})
	}
	if s.Sort != nil {
		q.Sort(s.Sort.Field, s.Sort.Desc)
	}
	if len(s.Rollups) > 0 {
		q.Rollups(s.Rollups...)
	}
	if s.Rows {
		q.Rows()
	}
	if s.Page != nil {
		q.Page(s.Page.Number, s.Page.Size)
	}
	if s.Limit > 0 {
		q.Limit(s.Limit)
	}
	return q, nil
}

// toRange maps a RangeSpec onto a query.Range. A nil receiver (no range set) is
// the zero Range = lifetime. lastN and between are mutually exclusive.
func (r *RangeSpec) toRange() (query.Range, error) {
	if r == nil {
		return query.Range{}, nil
	}
	if r.LastN != nil && r.Between != nil {
		return query.Range{}, fmt.Errorf("query: range must set lastN or between, not both")
	}
	if r.Between != nil {
		return query.Between(r.Between.Start, r.Between.End), nil
	}
	if r.LastN != nil {
		return query.LastN(*r.LastN), nil
	}
	return query.Range{}, nil
}

// toPredicate maps a PredicateNode tree onto a *query.Predicate tree. It is a
// pure structural transform: it copies kind/dim/op/values straight through so
// Compile (the single whitelist authority) is what rejects an unknown kind, op,
// or dimension. Children recurse.
func (n *PredicateNode) toPredicate() *query.Predicate {
	if n == nil {
		return nil
	}
	p := &query.Predicate{
		Kind:      query.PredKind(n.Kind),
		Dimension: n.Dim,
		Op:        query.Op(n.Op),
		Values:    n.Values,
	}
	for _, child := range n.Of {
		p.Of = append(p.Of, child.toPredicate())
	}
	return p
}
