// remap.go holds the query-time rename remap engine: RenameSets and the
// CASE-remap/re-group helpers applied to the aggregation queries.
package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ---- Query-time rename remap (non-destructive, reversible) ----
//
// A rename rule is applied at QUERY TIME only: raw heartbeats/projects/badges/
// rollup are never mutated. Dashboards SELECT/GROUP BY a CASE remap of the raw
// column (match_value -> new_value), which merges source values into the display
// value. Deleting the rule reverts dashboards instantly. Audit surfaces (Explorer
// group/list, latest, timeline) do NOT use the remap — they show the raw value.

// regexRename is one compiled-at-query-time regex rename (pattern -> new_value).
// For a template rule, newVal is a regexp_replace template (`\1`,`\2` backrefs).
type regexRename struct {
	pattern string
	newVal  string
}

// axisRenames holds an axis's rename rules split by match type. Exact rules are
// grouped by target (match -> new); regex rules are an ordered list; template
// rules are an ordered list of (pattern -> replacement-template) pairs.
type axisRenames struct {
	exact    map[string]string // match_value -> new_value
	regex    []regexRename     // pattern ~ -> new_value
	template []regexRename     // pattern ~ -> regexp_replace template
}

func (a axisRenames) empty() bool {
	return len(a.exact) == 0 && len(a.regex) == 0 && len(a.template) == 0
}

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

// ExactSourcesFor returns every raw value on the given axis that this rename
// map maps to `target` via an EXACT rule. Used by the widget-link path
// (gaka-xuc) so a scope pinned to a renamed/merged project name expands to
// the raw source names actually stored in heartbeats. Regex + template
// renames are intentionally ignored — reverse-engineering a pattern to enum
// its inputs is unreliable; the common merge case is exact-rules only.
//
// NOTE: exact keys are stored lowercased (case-insensitive matching), so the
// returned "raw" sources are lowercased. Callers that pass this to widget
// scopes should case-fold their raw comparisons or use the returned strings
// with the same lower() wrapping the SQL does at query time.
func (r RenameSets) ExactSourcesFor(axis, target string) []string {
	a, ok := r.byAxis[axis]
	if !ok || len(a.exact) == 0 {
		return nil
	}
	var out []string
	for raw, mapped := range a.exact {
		if mapped == target {
			out = append(out, raw)
		}
	}
	return out
}

// LoadRenameSets fetches the sender's rename rules (action='rename') per axis,
// split into exact and regex kinds. Exact match keys are lowercased so a stored
// rule for "Writing Docs" also fires on "writing docs" / "WRITING DOCS" at query
// time (the SQL side compares lower(col) = ANY(...) — see remapExpr). Regex and
// template patterns keep their original form; comparisons switch from `~` to
// `~*` (case-insensitive) so a pattern like `^Meet` matches `meet-*` too.
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
		switch mtype {
		case MatchRegex:
			a.regex = append(a.regex, regexRename{pattern: match, newVal: newv})
		case MatchTemplate:
			a.template = append(a.template, regexRename{pattern: match, newVal: newv})
		default:
			if a.exact == nil {
				a.exact = map[string]string{}
			}
			// Case-insensitive exact merge: keep lowercase key so raw variants
			// (Writing Docs / writing docs / WRITING DOCS) collapse to one entry
			// mapped to the same new value.
			a.exact[strings.ToLower(match)] = newv
		}
		rs.byAxis[axis] = a
	}
	return rs, rows.Err()
}

// remapExpr returns an SQL expression that maps `col` to its display value per the
// rename rules for `axis`, appending match/new values as $-params (injection safe).
// Exact rules use `lower(col) = ANY($arr)` grouped by target so case-variant sources
// (A / a / A ) merge into one WHEN; regex rules use `col ~* $pattern` (case-
// insensitive) THEN a fixed target; template rules use `col ~* $pattern` THEN
// `regexp_replace(col, $pattern, $template, 'i')` (capture-group backrefs, case
// insensitive). WHEN order is exact → regex → template (deterministic; first
// match wins per CASE). When the axis has no rules it returns `col` unchanged.
//
//	CASE WHEN lower(col) = ANY($arr) THEN $t
//	     [WHEN col ~* $pat THEN $t2 ...]
//	     [WHEN col ~* $pat THEN regexp_replace(col, $pat, $tmpl, 'i') ...]
//	ELSE col END
//
// extraCond, if non-empty, is ANDed into every WHEN (leaderboards scope the remap
// to the requester's own rows: `sender = $req`).
//
// Case-insensitivity note: the loader (LoadRenameSets) already lowercases the
// EXACT match keys, so the $arr param it binds here is a lowercase array; the
// SQL side compares `lower(col) = ANY($arr)`. Regex/template patterns are
// stored as authored — the case-insensitive `~*` flag is what makes them match
// mixed-case rows.
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
	// Sources are already lowercased by LoadRenameSets so a single lowercase
	// array param matches every case variant of the raw column.
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
		fmt.Fprintf(&b, "lower(%s) = ANY($%d)", col, nextArg)
		args = append(args, sources)
		nextArg++
		fmt.Fprintf(&b, " THEN $%d", nextArg)
		args = append(args, tgt)
		nextArg++
	}

	// Regex rules, in load order (rule id asc); first match wins (CASE semantics).
	// `~*` is Postgres's case-insensitive regex match — same semantics as `~` with
	// the `(?i)` flag, but without requiring users to embed it.
	for _, rr := range a.regex {
		whenPrefix(&b)
		fmt.Fprintf(&b, "%s ~* $%d", col, nextArg)
		args = append(args, rr.pattern)
		nextArg++
		fmt.Fprintf(&b, " THEN $%d", nextArg)
		args = append(args, rr.newVal)
		nextArg++
	}

	// Template rules, in load order. WHEN col ~* $pat THEN regexp_replace(col,$pat,$tmpl,'i').
	// The pattern is bound once as $p and reused in both the WHEN and the THEN so
	// only matching rows are rewritten (Postgres backrefs \1,\2 in the template).
	// The trailing 'i' flag makes both the match and the replacement case-
	// insensitive so the same rule catches every case variant of the source.
	for _, tr := range a.template {
		whenPrefix(&b)
		fmt.Fprintf(&b, "%s ~* $%d", col, nextArg)
		patArg := nextArg
		args = append(args, tr.pattern)
		nextArg++
		fmt.Fprintf(&b, " THEN regexp_replace(%s, $%d, $%d, 'i')", col, patArg, nextArg)
		args = append(args, tr.newVal)
		nextArg++
	}

	b.WriteString(" ELSE ")
	b.WriteString(col)
	b.WriteString(" END")
	return b.String(), args, nextArg
}

// statRowRemapAxes are the StatRow columns that carry a renamable axis (day is a
// passthrough grouping column; total_seconds is re-summed). Entity is included
// so case-variant file paths merge alongside the other axes — the entity
// aggregate view treats case-insensitively (per gaka case-insensitive-agg brief).
var statRowRemapAxes = []struct{ axis, col string }{
	{"project", "project"}, {"language", "language"}, {"editor", "editor"},
	{"branch", "branch"}, {"platform", "platform"}, {"machine", "machine"},
}

// caseFoldPick returns a SELECT expression that picks a single canonical display
// casing per case-folded group. MODE() WITHIN GROUP (ORDER BY x) returns the
// most common value in x — deterministic when ties are broken alphabetically by
// MODE's tie-break (Postgres picks the value with the smallest ordering).
// Applied per-axis on the RENAMED value so a rename that maps A/a/A to "M"
// still produces "M" (MODE over a constant is that constant).
func caseFoldPick(expr string) string {
	return fmt.Sprintf("MODE() WITHIN GROUP (ORDER BY %s)", expr)
}

// regroupStatRows wraps `inner` (a query that outputs the StatRow columns:
// day, project, language, editor, branch, platform, machine, entity,
// total_seconds, pct, daily_pct) in an outer re-group that applies the rename
// remap to the six renamable columns AND case-folds every axis (so
// "writing docs" / "Writing Docs" / "WRITING DOCS" merge into ONE row).
// Column ORDER matches scanStatRows exactly. nextArg is the first free
// positional param after the inner query's params.
//
// This wrap ALWAYS runs (even with zero rename rules) so that pure case
// variants — never mediated by curation — still merge. The GROUP BY key is
// lower(<remapped col>); the SELECT picks a canonical display via MODE().
func (rs RenameSets) regroupStatRows(inner string, nextArg int, args []any) (string, []any) {
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
        %s AS entity,
        CAST(SUM(total_seconds) AS int8) AS total_seconds
    FROM ( %s ) base
    GROUP BY day, lower(%s), lower(%s), lower(%s), lower(%s), lower(%s), lower(%s), lower(entity)
)
SELECT
    day, project, language, editor, branch, platform, machine, entity, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM regrouped`,
		caseFoldPick(exprs[0]), caseFoldPick(exprs[1]), caseFoldPick(exprs[2]),
		caseFoldPick(exprs[3]), caseFoldPick(exprs[4]), caseFoldPick(exprs[5]),
		caseFoldPick("entity"), inner,
		exprs[0], exprs[1], exprs[2], exprs[3], exprs[4], exprs[5])
	return q, args
}

// regroupProjectStatRows wraps a query outputting the ProjectStatRow columns
// (day, dayofweek, hourofday, language, entity, ty, total_seconds, pct, daily_pct)
// and remaps ONLY the language axis (the query is already project/tag scoped, and
// dayofweek/hourofday/ty are passthrough). Column order matches
// scanProjectStatRows. Case-folds language + entity — like regroupStatRows this
// wrap always runs so pure case variants merge with or without a rename.
func (rs RenameSets) regroupProjectStatRows(inner string, nextArg int, args []any) (string, []any) {
	inner = trimSQL(inner)
	var langExpr string
	langExpr, args, nextArg = rs.remapExpr("language", "language", "", nextArg, args)
	q := fmt.Sprintf(`WITH regrouped AS (
    SELECT
        day, dayofweek, hourofday,
        %s AS language,
        %s AS entity, ty,
        CAST(SUM(total_seconds) AS int8) AS total_seconds
    FROM ( %s ) base
    GROUP BY day, dayofweek, hourofday, lower(%s), lower(entity), ty
)
SELECT
    day, dayofweek, hourofday, language, entity, ty, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM regrouped`, caseFoldPick(langExpr), caseFoldPick("entity"), inner, langExpr)
	return q, args
}
