package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// exploreColumns maps FE-facing axis names to the real (trusted) SQL expression.
// This is the ONLY place a group/filter axis is turned into SQL — the raw client
// value is never interpolated, so the whitelist is the injection guard. Reused by
// both the group and rows endpoints (and reserved for future v2 remap/rename).
// It is a superset of the axis registry: every registry axis (raw column) plus
// the audit-only axes below.
var exploreColumns = func() map[string]string {
	m := map[string]string{
		"day":       "time_sent::date",
		"type":      "ty",
		"entity":    "entity",
		"isWrite":   "is_write",
		"userAgent": "user_agent",
	}
	for _, a := range axes {
		m[a.name] = a.rawCol
	}
	return m
}()

// ExploreColumn returns the trusted SQL expression for a whitelisted axis name
// and whether it is allowed. Exported for handler-side validation + tests.
func ExploreColumn(name string) (string, bool) {
	col, ok := exploreColumns[name]
	return col, ok
}

// ExploreFilter is a validated equality filter: Column is a trusted SQL
// expression (from the whitelist) and Value is the user-supplied comparand.
// A nil Value means "IS NULL".
type ExploreFilter struct {
	Column string
	Value  *string
}

// buildFilterClause appends "AND <col> = $n" (or "IS NULL") clauses for each
// filter, casting to text so heterogeneous column types (bool, date) compare
// against string params safely. Text-typed axes (every axis except day/is_write)
// are compared case-insensitively so the Explorer's drill-through — which
// forwards the raw label from the case-folded group listing — keeps its rows
// aligned with the case-folded dashboards. It starts numbering args at nextArg
// and returns the SQL fragment, the appended args, and the next free arg index.
func buildFilterClause(filters []ExploreFilter, nextArg int, args []any) (string, []any, int) {
	var b strings.Builder
	for _, f := range filters {
		if f.Value == nil {
			fmt.Fprintf(&b, " AND %s IS NULL", f.Column)
			continue
		}
		// day + is_write compare literally (date/bool cast to text); every other
		// whitelisted column carries a user-facing string that should fold case.
		if f.Column == "time_sent::date" || f.Column == "is_write" {
			fmt.Fprintf(&b, " AND %s::text = $%d", f.Column, nextArg)
		} else {
			fmt.Fprintf(&b, " AND lower(%s::text) = lower($%d)", f.Column, nextArg)
		}
		args = append(args, *f.Value)
		nextArg++
	}
	return b.String(), args, nextArg
}

// LatestHeartbeat returns MAX(time_sent) (UTC) and the total heartbeat count for
// a sender. last is nil when the user has no heartbeats. Fast: MAX uses the
// (sender, time_sent) index and count is owner-scoped.
func (d *DB) LatestHeartbeat(ctx context.Context, sender string) (last *time.Time, count int64, err error) {
	var maxTime *time.Time
	err = d.Pool.QueryRow(ctx,
		`SELECT max(time_sent), count(*) FROM heartbeats WHERE sender = $1`,
		sender).Scan(&maxTime, &count)
	if err != nil {
		return nil, 0, err
	}
	if maxTime != nil {
		u := maxTime.UTC()
		return &u, count, nil
	}
	return nil, count, nil
}

// ExploreGroup is one group bucket in the group response.
type ExploreGroup struct {
	Value     *string   `json:"value"` // null-able; "YYYY-MM-DD" for the day axis
	Count     int64     `json:"count"`
	Seconds   int64     `json:"seconds"` // attributed coding time (gap within timeLimit)
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// GroupHeartbeats groups a user's heartbeats by a whitelisted axis within a time
// range, applying any equality filters (the accumulated parent-group values).
// groupCol/filters must already be validated against the whitelist by the caller.
// Results are ordered by count desc and capped at limit; the second return value
// reports whether the result was truncated at the cap.
//
// entitySubstr is an optional case-insensitive substring on the entity column
// (ILIKE '%…%'); an empty string is a no-op. Same semantics as ListHeartbeats
// so the Explorer search box narrows BOTH the group listing AND the drilled
// leaf rows (previously it only narrowed leaves — gaka-90x sibling fix).
//
// seconds per group is SUM(gap_seconds) where gap_seconds <= limitMinutes*60,
// matching the dashboards' attributed-time convention. This is the AUDIT view:
// curation hide-exclusion is deliberately NOT applied (hidden values still show).
func (d *DB) GroupHeartbeats(ctx context.Context, sender, groupCol string, start, end time.Time, filters []ExploreFilter, entitySubstr string, limit int, limitMinutes int64) ([]ExploreGroup, bool, error) {
	// For the day axis, render the value as text 'YYYY-MM-DD'; otherwise cast the
	// column to text so every group value is a consistent string|null.
	//
	// Every string-valued axis is grouped case-insensitively — the group
	// displayed for entity/project/language/… is MODE()-picked so case variants
	// merge into ONE bucket matching the dashboards. day and is_write use their
	// natural literal comparison (date/bool).
	fold := groupCol != "time_sent::date" && groupCol != "is_write"
	valueExpr := groupCol + "::text"
	groupByExpr := groupCol
	if groupCol == "time_sent::date" {
		valueExpr = "to_char(time_sent::date, 'YYYY-MM-DD')"
	}
	if fold {
		valueExpr = fmt.Sprintf("MODE() WITHIN GROUP (ORDER BY %s::text)", groupCol)
		groupByExpr = fmt.Sprintf("lower(%s::text)", groupCol)
	}

	// $1 sender, $2 start, $3 end, then filter args, then the gap cutoff.
	args := []any{sender, start, end}
	filterSQL, args, nextArg := buildFilterClause(filters, 4, args)
	if entitySubstr != "" {
		filterSQL += fmt.Sprintf(" AND entity ILIKE $%d", nextArg)
		args = append(args, "%"+entitySubstr+"%")
		nextArg++
	}
	cutoffArg := nextArg
	args = append(args, limitMinutes)

	// Cap+1 so we can detect truncation.
	fetch := limit + 1
	query := fmt.Sprintf(`
		SELECT %s AS value,
		       count(*),
		       CAST(sum(CASE WHEN gap_seconds <= ($%d * 60) THEN gap_seconds ELSE 0 END) AS int8) AS seconds,
		       min(time_sent), max(time_sent)
		FROM heartbeats
		WHERE sender = $1 AND time_sent >= $2 AND time_sent <= $3%s
		GROUP BY %s
		ORDER BY count(*) DESC
		LIMIT %d`, valueExpr, cutoffArg, filterSQL, groupByExpr, fetch)

	rows, err := d.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := []ExploreGroup{}
	for rows.Next() {
		var g ExploreGroup
		if err := rows.Scan(&g.Value, &g.Count, &g.Seconds, &g.FirstSeen, &g.LastSeen); err != nil {
			return nil, false, err
		}
		out = append(out, g)
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

// ExploreRow is the full raw heartbeat record with FE JSON keys.
type ExploreRow struct {
	ID           int64     `json:"id"`
	Time         time.Time `json:"time"` // time_sent
	Entity       string    `json:"entity"`
	Type         string    `json:"type"` // ty
	Project      *string   `json:"project"`
	Language     *string   `json:"language"`
	Editor       *string   `json:"editor"`
	Plugin       *string   `json:"plugin"`
	Platform     *string   `json:"platform"`
	Machine      *string   `json:"machine"`
	Branch       *string   `json:"branch"`
	Category     *string   `json:"category"`
	IsWrite      *bool     `json:"isWrite"` // is_write
	Lineno       *int64    `json:"lineno"`
	Cursorpos    *string   `json:"cursorpos"` // TEXT column
	FileLines    *int64    `json:"fileLines"` // file_lines
	Dependencies []string  `json:"dependencies"`
	UserAgent    *string   `json:"userAgent"` // user_agent
}

// ListHeartbeats returns a page of raw heartbeats (newest first) plus the total
// row count for the same filters. filters must already be whitelist-validated.
// entitySubstr, when non-empty, applies an ILIKE '%..%' on entity.
func (d *DB) ListHeartbeats(ctx context.Context, sender string, start, end time.Time, filters []ExploreFilter, entitySubstr string, page, limit int) ([]ExploreRow, int64, error) {
	if page < 1 {
		page = 1
	}
	args := []any{sender, start, end}
	filterSQL, args, nextArg := buildFilterClause(filters, 4, args)

	if entitySubstr != "" {
		filterSQL += fmt.Sprintf(" AND entity ILIKE $%d", nextArg)
		args = append(args, "%"+entitySubstr+"%")
		nextArg++
	}

	where := fmt.Sprintf(`FROM heartbeats WHERE sender = $1 AND time_sent >= $2 AND time_sent <= $3%s`, filterSQL)

	// Total count for the same predicate.
	var total int64
	if err := d.Pool.QueryRow(ctx, "SELECT count(*) "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	listArgs := append(append([]any{}, args...), limit, offset)
	query := fmt.Sprintf(`
		SELECT id, time_sent, entity, ty, project, language, editor, plugin, platform,
		       machine, branch, category, is_write, lineno, cursorpos, file_lines,
		       dependencies, user_agent
		%s
		ORDER BY time_sent DESC
		LIMIT $%d OFFSET $%d`, where, nextArg, nextArg+1)

	rows, err := d.Pool.Query(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []ExploreRow{}
	for rows.Next() {
		var r ExploreRow
		if err := scanExploreRow(rows, &r); err != nil {
			return nil, 0, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func scanExploreRow(rows pgx.Rows, r *ExploreRow) error {
	// dependencies is text[]; scan into a []string that stays nil when NULL.
	var deps []string
	if err := rows.Scan(
		&r.ID, &r.Time, &r.Entity, &r.Type, &r.Project, &r.Language, &r.Editor,
		&r.Plugin, &r.Platform, &r.Machine, &r.Branch, &r.Category, &r.IsWrite,
		&r.Lineno, &r.Cursorpos, &r.FileLines, &deps, &r.UserAgent,
	); err != nil {
		return err
	}
	r.Dependencies = deps
	return nil
}
