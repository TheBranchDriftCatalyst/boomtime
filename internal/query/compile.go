package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier is the read subset of *pgxpool.Pool (and *pgx.Conn) that Run needs.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// havingOps whitelists the HAVING comparison operators.
var havingOps = map[string]string{
	">=": ">=", "<=": "<=", ">": ">", "<": "<", "==": "=", "!=": "<>",
}

// Compile validates a query against the domain registry and renders owner-scoped
// SQL + args. Every dimension/measure name is whitelisted → trusted SQL; every
// user value is a positional arg. Returns an error (and NO sql) the moment any
// name fails validation — an unknown/unsupported axis never reaches Postgres.
func Compile(owner string, q *Query) (string, []any, error) {
	if owner == "" {
		return "", nil, fmt.Errorf("query: empty owner")
	}
	dom, ok := lookupDomain(q.domain)
	if !ok {
		return "", nil, fmt.Errorf("query: unknown domain %q", q.domain)
	}
	if q.measure == "" {
		return "", nil, fmt.Errorf("query: no measure set")
	}
	m, ok := dom.Measures[q.measure]
	if !ok {
		return "", nil, fmt.Errorf("query: unknown measure %q on domain %q", q.measure, q.domain)
	}

	grouped := q.group != ""
	series := q.gran != GranNone
	if grouped && series {
		return "", nil, fmt.Errorf("query: cannot combine group(%q) with a time granularity in v1", q.group)
	}
	if q.bucket != nil && !grouped {
		return "", nil, fmt.Errorf("query: bucket policy requires a group dimension")
	}

	// value expression: always double precision, null-coalesced.
	valueExpr := fmt.Sprintf("COALESCE(%s, 0)::double precision", m.Expr)

	// SELECT + GROUP BY axis.
	var selectCols []string
	var groupByExpr string
	switch {
	case grouped:
		gd, ok := dom.Dimensions[q.group]
		if !ok {
			return "", nil, fmt.Errorf("query: unknown dimension %q on domain %q", q.group, q.domain)
		}
		// supportsDim is the same-table guard: a measure only whitelists dims
		// whose (trusted) Expr is valid on its own table. Dimension.Table is
		// descriptive; the Dims membership is authoritative.
		if !m.supportsDim(q.group) {
			return "", nil, fmt.Errorf("query: measure %q does not support group dimension %q", q.measure, q.group)
		}
		selectCols = append(selectCols, fmt.Sprintf("(%s)::text AS key", gd.Expr))
		groupByExpr = "(" + gd.Expr + ")::text"
	case series:
		field, ok := q.gran.trunc()
		if !ok {
			return "", nil, fmt.Errorf("query: unknown granularity %q", q.gran)
		}
		bucketExpr := fmt.Sprintf("date_trunc('%s', %s)", field, m.DateCol)
		selectCols = append(selectCols, bucketExpr+" AS bucket")
		groupByExpr = bucketExpr
	}
	selectCols = append(selectCols, valueExpr+" AS value")

	// WHERE: owner scope ($1), then the time range, then the predicate tree.
	args := []any{owner}
	next := 2
	where := []string{fmt.Sprintf("%s = $1", m.OwnerCol)}

	if !q.rng.isZero() {
		start, end, ok := resolveRange(q.anchor(), q.gran, q.rng)
		if ok {
			// half-open [start, endExclusive): endExclusive = end-date + 1 day so a
			// same-day timestamptz finish is included regardless of time-of-day.
			endExcl := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
			where = append(where, fmt.Sprintf("%s >= $%d AND %s < $%d", m.DateCol, next, m.DateCol, next+1))
			args = append(args, start, endExcl)
			next += 2
		}
	}

	if q.where != nil {
		frag, a2, n2, err := buildPredicate(q.where, m, dom, args, next)
		if err != nil {
			return "", nil, err
		}
		if frag != "" {
			where = append(where, frag)
		}
		args, next = a2, n2
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s\nFROM %s\nWHERE %s", strings.Join(selectCols, ", "), m.Table, strings.Join(where, " AND "))
	if groupByExpr != "" {
		fmt.Fprintf(&b, "\nGROUP BY %s", groupByExpr)
	}

	// HAVING on the aggregate.
	if q.having != nil {
		sqlOp, ok := havingOps[q.having.Op]
		if !ok {
			return "", nil, fmt.Errorf("query: unknown having op %q", q.having.Op)
		}
		if groupByExpr == "" {
			return "", nil, fmt.Errorf("query: having requires a group or time bucket")
		}
		fmt.Fprintf(&b, "\nHAVING COALESCE(%s, 0)::double precision %s $%d", m.Expr, sqlOp, next)
		args = append(args, q.having.Value)
		next++
	}

	// ORDER BY.
	orderBy, err := q.orderBy(grouped, series)
	if err != nil {
		return "", nil, err
	}
	if orderBy != "" {
		fmt.Fprintf(&b, "\nORDER BY %s", orderBy)
	}

	// LIMIT — skipped when a bucket policy owns the row set (applied in Go so the
	// pinned/top-N/Other roll-up sees every group first).
	if q.limit > 0 && q.bucket == nil {
		fmt.Fprintf(&b, "\nLIMIT %d", q.limit)
	}

	return b.String(), args, nil
}

// orderBy renders the ORDER BY clause. Scalar queries get none.
func (q *Query) orderBy(grouped, series bool) (string, error) {
	dir := func(desc bool) string {
		if desc {
			return "DESC"
		}
		return "ASC"
	}
	if q.sortSet {
		switch q.sortField {
		case "measure", "value":
			return "value " + dir(q.sortDesc), nil
		case "bucket":
			if !series {
				return "", fmt.Errorf("query: sort by bucket requires a time granularity")
			}
			return "bucket " + dir(q.sortDesc), nil
		case "key", q.group:
			if !grouped {
				return "", fmt.Errorf("query: sort by dimension requires a group")
			}
			return "key " + dir(q.sortDesc), nil
		default:
			return "", fmt.Errorf("query: unknown sort field %q", q.sortField)
		}
	}
	switch {
	case grouped:
		return "value DESC", nil // biggest groups first (bucket policy relies on this)
	case series:
		return "bucket ASC", nil // chronological
	default:
		return "", nil
	}
}

// buildPredicate renders a predicate node to SQL, appending args. It resolves
// every leaf dimension through the domain registry and rejects any the measure
// does not support (same-table guard). Values are always positional args.
func buildPredicate(p *Predicate, m Measure, dom Domain, args []any, next int) (string, []any, int, error) {
	switch p.Kind {
	case PredLeaf:
		d, ok := dom.Dimensions[p.Dimension]
		if !ok {
			return "", nil, 0, fmt.Errorf("query: unknown dimension %q on domain %q", p.Dimension, dom.Name)
		}
		if !m.supportsDim(p.Dimension) {
			return "", nil, 0, fmt.Errorf("query: measure %q does not support filter dimension %q", m.Name, p.Dimension)
		}
		if len(p.Values) == 0 {
			return "", nil, 0, fmt.Errorf("query: predicate on %q has no values", p.Dimension)
		}
		col := fmt.Sprintf("lower((%s)::text)", d.Expr)
		switch p.Op {
		case OpEq:
			frag := fmt.Sprintf("%s = lower($%d)", col, next)
			args = append(args, p.Values[0])
			return frag, args, next + 1, nil
		case OpNeq:
			frag := fmt.Sprintf("%s <> lower($%d)", col, next)
			args = append(args, p.Values[0])
			return frag, args, next + 1, nil
		case OpIn:
			ph := make([]string, len(p.Values))
			for i, v := range p.Values {
				ph[i] = fmt.Sprintf("lower($%d)", next)
				args = append(args, v)
				next++
			}
			return fmt.Sprintf("%s IN (%s)", col, strings.Join(ph, ", ")), args, next, nil
		default:
			return "", nil, 0, fmt.Errorf("query: unknown op %q", p.Op)
		}

	case PredAnd, PredOr:
		if len(p.Of) == 0 {
			return "", nil, 0, fmt.Errorf("query: %s predicate has no children", p.Kind)
		}
		joiner := " AND "
		if p.Kind == PredOr {
			joiner = " OR "
		}
		frags := make([]string, 0, len(p.Of))
		for _, child := range p.Of {
			f, a2, n2, err := buildPredicate(child, m, dom, args, next)
			if err != nil {
				return "", nil, 0, err
			}
			args, next = a2, n2
			frags = append(frags, f)
		}
		return "(" + strings.Join(frags, joiner) + ")", args, next, nil

	case PredNot:
		if len(p.Of) != 1 {
			return "", nil, 0, fmt.Errorf("query: not predicate requires exactly one child (got %d)", len(p.Of))
		}
		f, a2, n2, err := buildPredicate(p.Of[0], m, dom, args, next)
		if err != nil {
			return "", nil, 0, err
		}
		return "NOT (" + f + ")", a2, n2, nil

	default:
		return "", nil, 0, fmt.Errorf("query: unknown predicate kind %q", p.Kind)
	}
}

// anchor returns the range anchor (now), defaulting to time.Now().UTC().
func (q *Query) anchor() time.Time {
	if q.now.IsZero() {
		return time.Now().UTC()
	}
	return q.now
}

// resolveRange turns a Range into concrete inclusive [start,end] UTC dates.
// Explicit Start/End win; otherwise LastN steps back N units of the granularity
// (days when granularity is none). Returns ok=false only for a zero range.
func resolveRange(now time.Time, gran Granularity, r Range) (time.Time, time.Time, bool) {
	if r.isZero() {
		return time.Time{}, time.Time{}, false
	}
	if !r.Start.IsZero() || !r.End.IsZero() {
		return r.Start, r.End, true
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	n := r.LastN
	if n < 1 {
		n = 1
	}
	var start time.Time
	switch gran {
	case GranWeek:
		start = end.AddDate(0, 0, -7*(n-1))
		// widen to the ISO-week start so the first bucket is whole.
		start = weekStart(start)
	case GranMonth:
		start = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(n - 1), 0)
	default: // day or none
		start = end.AddDate(0, 0, -(n - 1))
	}
	return start, end, true
}

// weekStart returns the Monday (ISO week start, matching Postgres date_trunc
// 'week') of t's week, at UTC midnight.
func weekStart(t time.Time) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	// Go: Sunday=0..Saturday=6; ISO week starts Monday.
	wd := (int(d.Weekday()) + 6) % 7 // Monday=0 … Sunday=6
	return d.AddDate(0, 0, -wd)
}

// Run compiles the query, executes it, and shapes the typed Result.
func Run(ctx context.Context, db Querier, owner string, q *Query) (Result, error) {
	sqlText, args, err := Compile(owner, q)
	if err != nil {
		return Result{}, err
	}
	grouped := q.group != ""
	series := q.gran != GranNone

	switch {
	case grouped:
		rows, err := db.Query(ctx, sqlText, args...)
		if err != nil {
			return Result{}, err
		}
		defer rows.Close()
		var groups []Group
		for rows.Next() {
			var key *string
			var val float64
			if err := rows.Scan(&key, &val); err != nil {
				return Result{}, err
			}
			k := ""
			if key != nil {
				k = *key
			}
			groups = append(groups, Group{Key: k, Value: val})
		}
		if err := rows.Err(); err != nil {
			return Result{}, err
		}
		if q.bucket != nil {
			groups = applyBucketPolicy(groups, *q.bucket)
			if q.limit > 0 && len(groups) > q.limit {
				groups = groups[:q.limit]
			}
		}
		return Result{Kind: ResultGroups, Groups: groups}, nil

	case series:
		rows, err := db.Query(ctx, sqlText, args...)
		if err != nil {
			return Result{}, err
		}
		defer rows.Close()
		var pts []Point
		for rows.Next() {
			var bucket time.Time
			var val float64
			if err := rows.Scan(&bucket, &val); err != nil {
				return Result{}, err
			}
			pts = append(pts, Point{Bucket: bucket.UTC(), Value: val})
		}
		if err := rows.Err(); err != nil {
			return Result{}, err
		}
		return Result{Kind: ResultSeries, Series: pts}, nil

	default:
		var val float64
		if err := db.QueryRow(ctx, sqlText, args...).Scan(&val); err != nil {
			return Result{}, err
		}
		return Result{Kind: ResultScalar, Scalar: val}, nil
	}
}

// applyBucketPolicy keeps pinned + top-N groups (input MUST be sorted by value
// desc) and rolls the remainder into a single "Other" row. Pinned values are
// kept regardless of rank; Other is appended last.
func applyBucketPolicy(groups []Group, p BucketPolicy) []Group {
	pinned := make(map[string]bool, len(p.Pin))
	for _, v := range p.Pin {
		pinned[strings.ToLower(v)] = true
	}
	var kept []Group
	var other float64
	nonPinnedKept := 0
	for _, g := range groups {
		if pinned[strings.ToLower(g.Key)] {
			kept = append(kept, g)
			continue
		}
		if p.TopN <= 0 || nonPinnedKept < p.TopN {
			kept = append(kept, g)
			nonPinnedKept++
			continue
		}
		other += g.Value
	}
	// Stable: keep the value-desc order the SQL produced.
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Value > kept[j].Value })
	if p.Other && other > 0 {
		kept = append(kept, Group{Key: "Other", Value: other})
	}
	return kept
}
