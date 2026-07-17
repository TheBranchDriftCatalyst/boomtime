// predicates.go holds the hide-exclusion half of curation: HiddenSets and the
// exclusion predicates spliced into the aggregation queries.
package db

import (
	"context"
	"fmt"
	"strings"
)

// ---- Hide exclusion helpers ----

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

// exclusionPredicate builds `AND NOT (lower(<col>) = ANY($n))` clauses for each
// hidden axis present in cols (axis -> SQL column expression). The `lower()`
// wrap on the column mirrors the case-insensitive aggregation strategy: a hide
// rule for "MyProject" also drops rows where the raw column is "myproject" or
// "MYPROJECT". Values passed in the array param are already lowercased by
// LoadHiddenSets, so the ANY() comparison is a simple lowercase equality.
// Axes absent from cols are skipped (e.g. columns a pre-aggregated table
// lacks). scopeCond, if non-empty, is ANDed inside every NOT (mirrors
// remapExpr's extraCond; leaderboards scope the hide to the requester's own
// rows with `sender = $req`). Returns the SQL fragment, grown args, and next
// free arg index.
func exclusionPredicate(hs HiddenSets, cols map[string]string, scopeCond string, nextArg int, args []any) (string, []any, int) {
	var sql string
	for _, axis := range hiddenAxes { // deterministic order
		vals := hs.byAxis[axis]
		col := cols[axis]
		if len(vals) == 0 || col == "" {
			continue
		}
		if scopeCond != "" {
			sql += fmt.Sprintf(" AND NOT (%s AND lower(%s) = ANY($%d))", scopeCond, col, nextArg)
		} else {
			sql += fmt.Sprintf(" AND NOT (lower(%s) = ANY($%d))", col, nextArg)
		}
		args = append(args, vals)
		nextArg++
	}
	return sql, args, nextArg
}

// LoadHiddenSets fetches the hidden values for every dashboard-excluded axis in
// ONE query (grouped by axis in Go) instead of one query per axis. The axis
// filter keeps hide rules on non-dashboard axes (e.g. day) out of the sets,
// matching the per-axis loads this replaced.
//
// Match values are stored lowercase in memory so exclusionPredicate can compare
// against `lower(col)` — a hide rule authored as "MyProject" catches every
// case variant present in raw heartbeats.
func (d *DB) LoadHiddenSets(ctx context.Context, sender string) (HiddenSets, error) {
	hs := HiddenSets{byAxis: make(map[string][]string, len(hiddenAxes))}
	rows, err := d.Pool.Query(ctx,
		`SELECT axis, match_value FROM curation_rules
		 WHERE sender = $1 AND action = 'hide' AND axis = ANY($2)`,
		sender, hiddenAxes)
	if err != nil {
		return hs, err
	}
	defer rows.Close()
	for rows.Next() {
		var axis, v string
		if err := rows.Scan(&axis, &v); err != nil {
			return hs, err
		}
		hs.byAxis[axis] = append(hs.byAxis[axis], strings.ToLower(v))
	}
	return hs, rows.Err()
}
