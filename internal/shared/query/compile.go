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
	// Range shape is validated BEFORE the mode split so rows mode inherits it
	// too: a one-sided between window used to compile into an always-false date
	// filter and return an empty 200 instead of a 400.
	if err := q.rng.validate(); err != nil {
		return "", nil, err
	}

	// Leaf-rows mode is a distinct shape (no aggregate/measure): defer to the
	// rows compiler and return its page query for validation. Run issues the same
	// page query plus a count(*) for Total.
	if q.rowsMode {
		page, _, args, err := compileRowsSQL(owner, q, dom)
		return page, args, err
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

	// Multi-measure rollups: extra aggregates computed in the SAME grouped query
	// (one round-trip). count(*) is always first, then each requested rollup
	// measure. Each rollup name is registry-trusted (validated below) and its
	// Expr never carries user input; values still ride as args via the predicate.
	if len(q.rollups) > 0 {
		if !grouped {
			return "", nil, fmt.Errorf("query: rollups require a group dimension")
		}
		selectCols = append(selectCols, "count(*) AS count")
		for _, name := range q.rollups {
			rm, ok := dom.Measures[name]
			if !ok {
				return "", nil, fmt.Errorf("query: unknown rollup measure %q on domain %q", name, q.domain)
			}
			// Same-table rule (mirrors supportsDim): a rollup measure must share the
			// grouping measure's table/owner/date so its Expr is valid in the SAME
			// GROUP BY over the same owner-scoped, range-filtered row set.
			if rm.Table != m.Table || rm.OwnerCol != m.OwnerCol || rm.DateCol != m.DateCol {
				return "", nil, fmt.Errorf("query: rollup measure %q is not on the same table as measure %q", name, q.measure)
			}
			selectCols = append(selectCols, fmt.Sprintf("COALESCE(%s, 0)::double precision AS r_%s", rm.Expr, name))
		}
	}

	// WHERE: owner scope ($1), then the time range, then the predicate tree.
	args := []any{owner}
	next := 2
	where := []string{fmt.Sprintf("%s = $1", m.OwnerCol)}

	if !q.rng.isZero() {
		start, end, ok := resolveRange(q.anchor(), q.gran, q.rng)
		if ok {
			// half-open [start, endExclusive): endExclusive = end-date + 1 day so a
			// same-day timestamptz finish is included regardless of time-of-day.
			endExcl := endExclusive(end)
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

// compileRowsSQL renders the leaf-rows page query + a matching count(*) query,
// sharing ONE args slice (owner $1, range, predicate) so both filter identically.
// The owner scope + injection guarantee are the aggregate path's verbatim: the
// owner is pinned to $1, the range/predicate values are positional args, and the
// projected column Exprs + DefaultSort are registry-trusted (never user input).
// LIMIT/OFFSET are ints we own (validated non-negative), inlined like the
// aggregate path's LIMIT.
func compileRowsSQL(owner string, q *Query, dom Domain) (pageSQL, countSQL string, args []any, err error) {
	if owner == "" {
		return "", "", nil, fmt.Errorf("query: empty owner")
	}
	rs := dom.Rows
	if rs == nil {
		return "", "", nil, fmt.Errorf("query: domain %q does not support row listing", q.domain)
	}
	if q.group != "" {
		return "", "", nil, fmt.Errorf("query: rows mode cannot be combined with a group dimension")
	}
	if q.gran != GranNone {
		return "", "", nil, fmt.Errorf("query: rows mode cannot be combined with a time granularity")
	}
	// Rows mode reads ONLY rng/where/page below. Everything else in the spec is
	// aggregate-path machinery that this compiler has no expression for, and it
	// used to be dropped in silence: a client asking for {rows:true, sort:{...},
	// limit:10} got a 200 whose rows were neither sorted by that field nor
	// capped at 10, with nothing anywhere saying so. Reject them the same way
	// group/granularity are already rejected — a wrong 200 is worse than a 400.
	//
	// `measure` is deliberately NOT rejected: both books explorer configs send
	// it alongside rows:true because the FE spec type requires it (see
	// booksExplorerConfig.tsx / readingEventsExplorerConfig.tsx, "ignored in
	// rows mode"), so rejecting it would 400 every leaf page in the library.
	// It is ignored, but it is ignored by documented contract, not by accident.
	if q.sortSet {
		return "", "", nil, fmt.Errorf("query: rows mode does not support sort (leaf rows use the row source's fixed ordering)")
	}
	if q.limit > 0 {
		return "", "", nil, fmt.Errorf("query: rows mode does not support limit — use page {number, size}")
	}
	if q.having != nil {
		return "", "", nil, fmt.Errorf("query: rows mode cannot be combined with having (there is no aggregate to filter)")
	}
	if q.bucket != nil {
		return "", "", nil, fmt.Errorf("query: rows mode cannot be combined with a bucket policy")
	}
	if len(q.rollups) > 0 {
		return "", "", nil, fmt.Errorf("query: rows mode cannot be combined with rollups")
	}
	if len(rs.Columns) == 0 {
		return "", "", nil, fmt.Errorf("query: domain %q row source has no columns", q.domain)
	}

	// Projection: each trusted Expr AS its QUOTED output name so Postgres returns
	// the field name byte-exact (an unquoted camelCase alias would lower-fold and
	// break the FE key mapping).
	cols := make([]string, len(rs.Columns))
	for i, c := range rs.Columns {
		cols[i] = fmt.Sprintf(`%s AS "%s"`, c.Expr, c.Name)
	}

	// WHERE: owner ($1), then range, then the predicate tree — identical
	// construction + arg order to the aggregate path, reusing buildPredicate via
	// a synthetic same-table measure so the same-table whitelist still applies.
	args = []any{owner}
	next := 2
	where := []string{fmt.Sprintf("%s = $1", rs.OwnerCol)}

	if !q.rng.isZero() {
		start, end, ok := resolveRange(q.anchor(), q.gran, q.rng)
		if ok {
			endExcl := endExclusive(end)
			where = append(where, fmt.Sprintf("%s >= $%d AND %s < $%d", rs.DateCol, next, rs.DateCol, next+1))
			args = append(args, start, endExcl)
			next += 2
		}
	}

	if q.where != nil {
		rm, _ := rowsMeasure(dom)
		frag, a2, n2, perr := buildPredicate(q.where, rm, dom, args, next)
		if perr != nil {
			return "", "", nil, perr
		}
		if frag != "" {
			where = append(where, frag)
		}
		args, next = a2, n2
	}
	whereSQL := strings.Join(where, " AND ")

	// Pagination: 1-based page, positive size (default 50).
	page, size := q.page, q.pageSize
	if size <= 0 {
		size = 50
	}
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * size

	order := ""
	if rs.DefaultSort != "" {
		order = "\nORDER BY " + rs.DefaultSort
	}
	pageSQL = fmt.Sprintf("SELECT %s\nFROM %s\nWHERE %s%s\nLIMIT %d OFFSET %d",
		strings.Join(cols, ", "), rs.Table, whereSQL, order, size, offset)
	countSQL = fmt.Sprintf("SELECT count(*)\nFROM %s\nWHERE %s", rs.Table, whereSQL)
	return pageSQL, countSQL, args, nil
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

// escapeLikeLiteral makes a user value safe to use as a LITERAL substring
// inside an ILIKE pattern: it backslash-escapes the two LIKE metacharacters
// ('%' = any run, '_' = any single char) and the escape character itself.
//
// This is NOT an injection defence — the value has always ridden as a
// positional arg — it is a SEMANTICS fix. `ilike` is documented as "contains
// this substring", but an unescaped '_' or '%' turned the search into a
// wildcard match, so searching the books explorer for "catalyst_ui" also
// returned "catalyst-ui" and "50%" returned every title containing "50".
// Backslash is Postgres' default LIKE escape character, so the pattern needs
// no ESCAPE clause (and adding one would hard-code a literal backslash into
// the SQL text, which is exactly what we're avoiding).
//
// Order matters: the backslash must be doubled FIRST, or the backslashes this
// function introduces would themselves be escaped.
func escapeLikeLiteral(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(v)
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
		case OpILike:
			// Case-insensitive substring match. ILIKE is already case-folding, so we
			// match the raw text-cast dim expr (NOT the lower()-wrapped `col`) against
			// '%value%'. The value rides as a positional arg concatenated at runtime by
			// Postgres (`'%' || $n || '%'`) — the % wildcards are literal SQL, the user
			// value never is, so this is injection-safe exactly like eq/in. Multiple
			// values OR together ("contains ANY of these substrings").
			//
			// The value is ESCAPED first (escapeLikeLiteral): '%' and '_' are LIKE
			// metacharacters, so an unescaped search for "catalyst_ui" also matched
			// "catalyst-ui"/"catalystXui" and "50%" matched "50" followed by
			// anything. Escaping restores the documented "contains this literal
			// substring" semantics. Postgres' default LIKE escape character is
			// backslash, so no ESCAPE clause is needed and the SQL shape is
			// unchanged.
			exprText := fmt.Sprintf("(%s)::text", d.Expr)
			ph := make([]string, len(p.Values))
			for i := range p.Values {
				ph[i] = fmt.Sprintf("%s ILIKE ('%%' || $%d || '%%')", exprText, next)
				args = append(args, escapeLikeLiteral(p.Values[i]))
				next++
			}
			if len(ph) == 1 {
				return ph[0], args, next, nil
			}
			return "(" + strings.Join(ph, " OR ") + ")", args, next, nil
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

// endExclusive turns the caller's INCLUSIVE end bound into the half-open upper
// bound the WHERE clause uses: the end DATE + 1 day, at UTC midnight.
//
// The date is taken in UTC (end.UTC()), not in whatever offset the client's
// timestamp carried. Both bounds are already compared against a timestamptz
// column in UTC, so reading Y/M/D out of a non-UTC offset truncated the window:
// end="2026-01-15T23:00:00-05:00" is the instant 2026-01-16T04:00Z, but the
// offset-local date is Jan 15, which produced endExcl=Jan 16 00:00Z and dropped
// every row between 00:00Z and 04:00Z on Jan 16 — rows INSIDE the inclusive
// window the caller asked for. Normalizing to UTC first keeps the "+1 day"
// widening honest: the bound is never earlier than the instant requested.
func endExclusive(end time.Time) time.Time {
	u := end.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
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
	// Leaf-rows mode: a page query + a matching count(*), sharing one args slice.
	if q.rowsMode {
		dom, ok := lookupDomain(q.domain)
		if !ok {
			return Result{}, fmt.Errorf("query: unknown domain %q", q.domain)
		}
		pageSQL, countSQL, args, err := compileRowsSQL(owner, q, dom)
		if err != nil {
			return Result{}, err
		}
		pr, err := db.Query(ctx, pageSQL, args...)
		if err != nil {
			return Result{}, err
		}
		out, err := pgx.CollectRows(pr, pgx.RowToMap) // closes pr
		if err != nil {
			return Result{}, err
		}
		var total int
		if err := db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
			return Result{}, err
		}
		return Result{Kind: ResultRows, Rows: out, Total: total}, nil
	}

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
			if len(q.rollups) > 0 {
				// Columns: key, value, count, r_<rollup>… — scan the rollups into Stats.
				var count int64
				rvals := make([]float64, len(q.rollups))
				dest := make([]any, 0, 3+len(rvals))
				dest = append(dest, &key, &val, &count)
				for i := range rvals {
					dest = append(dest, &rvals[i])
				}
				if err := rows.Scan(dest...); err != nil {
					return Result{}, err
				}
				stats := make(map[string]float64, len(rvals)+1)
				stats["count"] = float64(count)
				for i, name := range q.rollups {
					stats[name] = rvals[i]
				}
				k := ""
				if key != nil {
					k = *key
				}
				groups = append(groups, Group{Key: k, Value: val, Stats: stats})
				continue
			}
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
	var otherStats map[string]float64 // nil until a rolled row carries Stats
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
		for k, v := range g.Stats {
			if otherStats == nil {
				otherStats = map[string]float64{}
			}
			otherStats[k] += v
		}
	}
	// Stable: keep the value-desc order the SQL produced.
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Value > kept[j].Value })
	if p.Other && (other > 0 || otherStats != nil) {
		kept = append(kept, Group{Key: "Other", Value: other, Stats: otherStats})
	}
	return kept
}
