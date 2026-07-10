package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Curation actions and match types.
const (
	CurationHide   = "hide"
	CurationRename = "rename"

	MatchExact = "exact"
	MatchRegex = "regex"
)

// CurationRule is a per-user data-curation rule (hide or rename) on a label axis.
// MatchType is "exact" (MatchValue is a literal) or "regex" (MatchValue is a
// Postgres regex applied to the raw column via ~).
type CurationRule struct {
	ID         int       `json:"id"`
	Axis       string    `json:"axis"`
	Action     string    `json:"action"`
	MatchType  string    `json:"matchType"`
	MatchValue string    `json:"matchValue"`
	NewValue   *string   `json:"newValue"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListCurationRules returns a user's rules, newest first.
func (d *DB) ListCurationRules(ctx context.Context, sender string) ([]CurationRule, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, axis, action, match_type, match_value, new_value, created_at
		FROM curation_rules WHERE sender = $1 ORDER BY id DESC`, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CurationRule{}
	for rows.Next() {
		var r CurationRule
		if err := rows.Scan(&r.ID, &r.Axis, &r.Action, &r.MatchType, &r.MatchValue, &r.NewValue, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateCurationRule inserts a rule (deduped on sender,axis,action,match_type,
// match_value) and returns it. On an existing duplicate it updates new_value.
func (d *DB) CreateCurationRule(ctx context.Context, sender, axis, action, matchType, matchValue string, newValue *string) (*CurationRule, error) {
	if matchType == "" {
		matchType = MatchExact
	}
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO curation_rules (sender, axis, action, match_type, match_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (sender, axis, action, match_type, match_value)
		DO UPDATE SET new_value = EXCLUDED.new_value
		RETURNING id, axis, action, match_type, match_value, new_value, created_at`,
		sender, axis, action, matchType, matchValue, newValue)
	var r CurationRule
	if err := row.Scan(&r.ID, &r.Axis, &r.Action, &r.MatchType, &r.MatchValue, &r.NewValue, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetCurationRule fetches a single rule by id (no owner filter; caller checks).
func (d *DB) GetCurationRule(ctx context.Context, id int) (*CurationRule, string, error) {
	var r CurationRule
	var sender string
	err := d.Pool.QueryRow(ctx, `
		SELECT id, axis, action, match_type, match_value, new_value, created_at, sender
		FROM curation_rules WHERE id = $1`, id).
		Scan(&r.ID, &r.Axis, &r.Action, &r.MatchType, &r.MatchValue, &r.NewValue, &r.CreatedAt, &sender)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &r, sender, nil
}

// DeleteCurationRule removes a rule (owner-scoped). Returns rows affected.
func (d *DB) DeleteCurationRule(ctx context.Context, sender string, id int) (int64, error) {
	ct, err := d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE id = $1 AND sender = $2`, id, sender)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// AffectedValue is one distinct RAW value a rule matches, with its heartbeat count.
type AffectedValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// CurationAffectedValues returns the DISTINCT RAW values (with heartbeat counts)
// that a rule matches on its axis, owner-scoped and UNFILTERED (audit). Exact
// rules match the single literal; regex rules match every value where the raw
// column ~ the pattern. Ordered by count desc, capped at `limit`; the second
// return reports truncation. Injection-safe: the axis maps to a whitelisted
// column and match_value is a bound param.
func (d *DB) CurationAffectedValues(ctx context.Context, sender string, rule *CurationRule, limit int) ([]AffectedValue, bool, error) {
	col, ok := rawHeartbeatCols[rule.Axis]
	if !ok {
		// Non-remap axes (e.g. day/entity for hide) have no heartbeats column here.
		if c, whok := ExploreColumn(rule.Axis); whok {
			col = c // e.g. "time_sent::date" for day, "entity", "ty"
		} else {
			return nil, false, fmt.Errorf("axis %q has no affected-values column", rule.Axis)
		}
	}
	if limit <= 0 {
		limit = 200
	}

	pred := col + " = $2"
	if rule.MatchType == MatchRegex {
		pred = col + " ~ $2"
	}
	// Fetch limit+1 to detect truncation.
	q := fmt.Sprintf(`
		SELECT %s::text AS value, count(*) AS cnt
		FROM heartbeats
		WHERE sender = $1 AND %s IS NOT NULL AND %s
		GROUP BY %s
		ORDER BY cnt DESC, value ASC
		LIMIT %d`, col, col, pred, col, limit+1)

	rows, err := d.Pool.Query(ctx, q, sender, rule.MatchValue)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []AffectedValue{}
	for rows.Next() {
		var v AffectedValue
		if err := rows.Scan(&v.Value, &v.Count); err != nil {
			return nil, false, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := false
	if len(out) > limit {
		out = out[:limit]
		truncated = true
	}
	return out, truncated, nil
}

// ValidateRegex checks that a pattern compiles as a Postgres regex (guarded).
// Returns nil when valid, else a user-facing error.
func (d *DB) ValidateRegex(ctx context.Context, pattern string) error {
	var ok bool
	// `'' ~ $1` forces Postgres to compile the pattern without scanning any rows.
	err := d.Pool.QueryRow(ctx, `SELECT ''::text ~ $1`, pattern).Scan(&ok)
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	return nil
}

// ---- Query-time rename remap (non-destructive, reversible) ----
//
// A rename rule is applied at QUERY TIME only: raw heartbeats/projects/badges/
// rollup are never mutated. Dashboards SELECT/GROUP BY a CASE remap of the raw
// column (match_value -> new_value), which merges source values into the display
// value. Deleting the rule reverts dashboards instantly. Audit surfaces (Explorer
// group/list, latest, timeline) do NOT use the remap — they show the raw value.

// regexRename is one compiled-at-query-time regex rename (pattern -> new_value).
type regexRename struct {
	pattern string
	newVal  string
}

// axisRenames holds an axis's rename rules split by match type. Exact rules are
// grouped by target (match -> new); regex rules are an ordered list.
type axisRenames struct {
	exact map[string]string // match_value -> new_value
	regex []regexRename     // pattern ~ -> new_value
}

func (a axisRenames) empty() bool { return len(a.exact) == 0 && len(a.regex) == 0 }

// RenameSets holds the sender's active rename rules per axis.
type RenameSets struct {
	byAxis map[string]axisRenames
}

// Any reports whether the sender has any rename rule.
func (r RenameSets) Any() bool {
	for _, a := range r.byAxis {
		if !a.empty() {
			return true
		}
	}
	return false
}

// HasAxis reports whether any rename rule (exact or regex) targets the given axis.
func (r RenameSets) HasAxis(axis string) bool {
	a, ok := r.byAxis[axis]
	return ok && !a.empty()
}

// LoadRenameSets fetches the sender's rename rules (action='rename') per axis,
// split into exact and regex kinds.
func (d *DB) LoadRenameSets(ctx context.Context, sender string) (RenameSets, error) {
	rs := RenameSets{byAxis: map[string]axisRenames{}}
	rows, err := d.Pool.Query(ctx,
		`SELECT axis, match_type, match_value, new_value FROM curation_rules
		 WHERE sender = $1 AND action = 'rename' AND new_value IS NOT NULL
		 ORDER BY id ASC`, sender)
	if err != nil {
		return rs, err
	}
	defer rows.Close()
	for rows.Next() {
		var axis, mtype, match, newv string
		if err := rows.Scan(&axis, &mtype, &match, &newv); err != nil {
			return rs, err
		}
		a := rs.byAxis[axis]
		if mtype == MatchRegex {
			a.regex = append(a.regex, regexRename{pattern: match, newVal: newv})
		} else {
			if a.exact == nil {
				a.exact = map[string]string{}
			}
			a.exact[match] = newv
		}
		rs.byAxis[axis] = a
	}
	return rs, rows.Err()
}

// remapExpr returns an SQL expression that maps `col` to its display value per the
// rename rules for `axis`, appending match/new values as $-params (injection safe).
// Exact rules use `col = ANY($arr)` grouped by target so A,B→M collapse into one
// WHEN; regex rules use `col ~ $pattern` (one WHEN each). Exact WHENs precede
// regex WHENs (deterministic ordering). When the axis has no rules it returns
// `col` unchanged with no new args.
//
//	CASE WHEN col = ANY($arr) THEN $t [WHEN col ~ $pat THEN $t2 ...] ELSE col END
//
// extraCond, if non-empty, is ANDed into every WHEN (leaderboards scope the remap
// to the requester's own rows: `sender = $req`).
func (r RenameSets) remapExpr(axis, col, extraCond string, nextArg int, args []any) (string, []any, int) {
	a := r.byAxis[axis]
	if a.empty() {
		return col, args, nextArg
	}

	whenPrefix := func(b *strings.Builder) {
		b.WriteString(" WHEN ")
		if extraCond != "" {
			b.WriteString(extraCond)
			b.WriteString(" AND ")
		}
	}

	var b strings.Builder
	b.WriteString("CASE")

	// Exact rules, grouped by target (deterministic target + source ordering).
	byTarget := map[string][]string{}
	for match, tgt := range a.exact {
		byTarget[tgt] = append(byTarget[tgt], match)
	}
	targets := make([]string, 0, len(byTarget))
	for t := range byTarget {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	for _, tgt := range targets {
		sources := byTarget[tgt]
		sort.Strings(sources)
		whenPrefix(&b)
		fmt.Fprintf(&b, "%s = ANY($%d)", col, nextArg)
		args = append(args, sources)
		nextArg++
		fmt.Fprintf(&b, " THEN $%d", nextArg)
		args = append(args, tgt)
		nextArg++
	}

	// Regex rules, in load order (rule id asc); first match wins (CASE semantics).
	for _, rr := range a.regex {
		whenPrefix(&b)
		fmt.Fprintf(&b, "%s ~ $%d", col, nextArg)
		args = append(args, rr.pattern)
		nextArg++
		fmt.Fprintf(&b, " THEN $%d", nextArg)
		args = append(args, rr.newVal)
		nextArg++
	}

	b.WriteString(" ELSE ")
	b.WriteString(col)
	b.WriteString(" END")
	return b.String(), args, nextArg
}

// trimSQL strips trailing whitespace and a trailing ';' so a query can be safely
// embedded as a subquery `( <inner> ) base`.
func trimSQL(q string) string {
	return strings.TrimRight(strings.TrimSpace(q), ";")
}

// statRowRemapAxes are the StatRow columns that carry a renamable axis (day and
// entity are passthrough grouping columns; total_seconds is re-summed).
var statRowRemapAxes = []struct{ axis, col string }{
	{"project", "project"}, {"language", "language"}, {"editor", "editor"},
	{"branch", "branch"}, {"platform", "platform"}, {"machine", "machine"},
}

// regroupStatRows wraps `inner` (a query that outputs the StatRow columns:
// day, project, language, editor, branch, platform, machine, entity,
// total_seconds, pct, daily_pct) in an outer re-group that applies the rename
// remap to the six renamable columns, re-sums total_seconds, and recomputes the
// pct/daily_pct windows. Column ORDER matches scanStatRows exactly. nextArg is the
// first free positional param after the inner query's params. When no rename
// applies it returns `inner` unchanged.
func (rs RenameSets) regroupStatRows(inner string, nextArg int, args []any) (string, []any) {
	if !rs.Any() {
		return inner, args
	}
	inner = trimSQL(inner)
	exprs := make([]string, len(statRowRemapAxes))
	for i, a := range statRowRemapAxes {
		var e string
		e, args, nextArg = rs.remapExpr(a.axis, a.col, "", nextArg, args)
		exprs[i] = e
	}
	q := fmt.Sprintf(`WITH regrouped AS (
    SELECT
        day,
        %s AS project,
        %s AS language,
        %s AS editor,
        %s AS branch,
        %s AS platform,
        %s AS machine,
        entity,
        CAST(SUM(total_seconds) AS int8) AS total_seconds
    FROM ( %s ) base
    GROUP BY day, %s, %s, %s, %s, %s, %s, entity
)
SELECT
    day, project, language, editor, branch, platform, machine, entity, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM regrouped`,
		exprs[0], exprs[1], exprs[2], exprs[3], exprs[4], exprs[5], inner,
		exprs[0], exprs[1], exprs[2], exprs[3], exprs[4], exprs[5])
	return q, args
}

// regroupProjectStatRows wraps a query outputting the ProjectStatRow columns
// (day, dayofweek, hourofday, language, entity, ty, total_seconds, pct, daily_pct)
// and remaps ONLY the language axis (the query is already project/tag scoped, and
// dayofweek/hourofday/entity/ty are passthrough). Column order matches
// scanProjectStatRows. Returns `inner` unchanged when no language rename applies.
func (rs RenameSets) regroupProjectStatRows(inner string, nextArg int, args []any) (string, []any) {
	if !rs.HasAxis("language") {
		return inner, args
	}
	inner = trimSQL(inner)
	var langExpr string
	langExpr, args, nextArg = rs.remapExpr("language", "language", "", nextArg, args)
	q := fmt.Sprintf(`WITH regrouped AS (
    SELECT
        day, dayofweek, hourofday,
        %s AS language,
        entity, ty,
        CAST(SUM(total_seconds) AS int8) AS total_seconds
    FROM ( %s ) base
    GROUP BY day, dayofweek, hourofday, %s, entity, ty
)
SELECT
    day, dayofweek, hourofday, language, entity, ty, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM regrouped`, langExpr, inner, langExpr)
	return q, args
}

// ---- Hide exclusion helpers ----

// HiddenValues returns the set of hidden match_values for one axis (action=hide).
func (d *DB) HiddenValues(ctx context.Context, sender, axis string) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT match_value FROM curation_rules WHERE sender = $1 AND axis = $2 AND action = 'hide'`,
		sender, axis)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// hiddenAxes is the definitive set of curation-hide axes excluded from the
// aggregate dashboards. Suppressing a value on any of these axes removes it from
// stats/projects/big-bet dashboards. Ordered for deterministic SQL/arg building.
var hiddenAxes = []string{
	"project", "language", "editor", "plugin", "machine", "platform", "branch", "category",
}

// HiddenSets holds the hidden values per axis (axis name -> match_values). Empty
// or absent slices mean "exclude nothing" for that axis.
type HiddenSets struct {
	byAxis map[string][]string
}

// Values returns the hidden values for one axis (nil if none).
func (h HiddenSets) Values(axis string) []string { return h.byAxis[axis] }

// Projects is a convenience accessor for the project axis (project-only paths).
func (h HiddenSets) Projects() []string { return h.byAxis["project"] }

// AnyHidden reports whether any axis has a hidden value.
func (h HiddenSets) AnyHidden() bool {
	for _, v := range h.byAxis {
		if len(v) > 0 {
			return true
		}
	}
	return false
}

// HasHiddenOutside reports whether any hidden axis is NOT in the provided
// available set. Used to decide whether a pre-aggregated path (e.g. the rollup,
// which lacks some columns) must fall back to the raw heartbeats scan.
func (h HiddenSets) HasHiddenOutside(available map[string]bool) bool {
	for axis, vals := range h.byAxis {
		if len(vals) > 0 && !available[axis] {
			return true
		}
	}
	return false
}

// exclusionPredicate builds `AND NOT (<col> = ANY($n))` clauses for each hidden
// axis present in cols (axis -> SQL column expression). Axes absent from cols are
// skipped (e.g. columns a pre-aggregated table lacks). Values are passed as array
// params, so this is injection-safe. Returns the SQL fragment, grown args, and
// next free arg index.
func exclusionPredicate(hs HiddenSets, cols map[string]string, nextArg int, args []any) (string, []any, int) {
	var sql string
	for _, axis := range hiddenAxes { // deterministic order
		vals := hs.byAxis[axis]
		col := cols[axis]
		if len(vals) == 0 || col == "" {
			continue
		}
		sql += fmt.Sprintf(" AND NOT (%s = ANY($%d))", col, nextArg)
		args = append(args, vals)
		nextArg++
	}
	return sql, args, nextArg
}

// rawHeartbeatCols maps every hidden axis to its column on the raw heartbeats
// table. Used by all queries whose innermost scan is `heartbeats` (all axes are
// available). `type` is stored in the ty column but is not a hide axis here.
var rawHeartbeatCols = map[string]string{
	"project": "project", "language": "language", "editor": "editor",
	"plugin": "plugin", "machine": "machine", "platform": "platform",
	"branch": "branch", "category": "category",
}

// LoadHiddenSets fetches the hidden values for every dashboard-excluded axis.
func (d *DB) LoadHiddenSets(ctx context.Context, sender string) (HiddenSets, error) {
	hs := HiddenSets{byAxis: make(map[string][]string, len(hiddenAxes))}
	for _, axis := range hiddenAxes {
		vals, err := d.HiddenValues(ctx, sender, axis)
		if err != nil {
			return hs, err
		}
		if len(vals) > 0 {
			hs.byAxis[axis] = vals
		}
	}
	return hs, nil
}
