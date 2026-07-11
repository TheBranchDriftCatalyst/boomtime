// splice.go holds the shared aggregation plumbing: the work_mem-elevated
// aggQuery, SQL splicing/trimming helpers, and row scanners/axis maps shared by
// the aggregation queries.
package db

import (
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// statsWorkMem is applied via `SET LOCAL work_mem` to the heavy aggregation
// queries so their sorts stay in RAM instead of spilling to disk (on ~438k rows
// the disk spill roughly doubled latency). It is transaction-scoped, so it never
// leaks to other pooled connections. Tunable via BOOM_STATS_WORK_MEM (e.g. 128MB).
var statsWorkMem = "256MB"

var workMemPattern = regexp.MustCompile(`^[0-9]+(kB|MB|GB)$`)

func init() {
	if v := os.Getenv("BOOM_STATS_WORK_MEM"); workMemPattern.MatchString(v) {
		statsWorkMem = v
	}
}

// aggQuery runs a read-only aggregation query inside a transaction with an
// elevated work_mem, handing the rows to scan (which must consume/close them).
// The SET LOCAL is discarded by the read-only rollback.
func (d *DB) aggQuery(ctx context.Context, sql string, args []any, scan func(pgx.Rows) error) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL work_mem = '"+statsWorkMem+"'"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	return scan(rows)
}

// numToFloat converts a scanned pgtype.Numeric to float64 (numeric(13,12) percentages).
func numToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// injectAfter splices addition into query immediately after the first occurrence
// of anchor. If anchor is absent it returns query unchanged (so a drifted .sql is
// caught by the exclusion tests rather than producing broken SQL).
func injectAfter(query, anchor, addition string) string {
	if addition == "" {
		return query
	}
	idx := strings.Index(query, anchor)
	if idx < 0 {
		return query
	}
	pos := idx + len(anchor)
	return query[:pos] + addition + query[pos:]
}

// applyScopes splices the two per-request row filters into an aggregation query
// immediately after `anchor`: the curation hide exclusion (action=hide) and the
// Space inclusion scope (?space=), preserving spaceScopePredicate's empty-space
// match-nothing (` AND FALSE`) semantics. cols maps each axis to the query's
// column expression (raw, rollup, or join-qualified). Either filter is a no-op
// when inactive, so single-filter callers pass a zero HiddenSets or MemberSets.
// Returns the grown query/args and the next free positional param (for a
// following rename remap).
func applyScopes(query, anchor string, hs HiddenSets, ms MemberSets, spaceRequested bool, cols map[string]string, args []any, next int) (string, []any, int) {
	if hs.AnyHidden() {
		var pred string
		pred, args, next = exclusionPredicate(hs, cols, "", next, args)
		query = injectAfter(query, anchor, pred)
	}
	if spaceRequested {
		var pred string
		pred, args, next = spaceScopePredicate(ms, cols, "", next, args, spaceRequested)
		query = injectAfter(query, anchor, pred)
	}
	return query, args, next
}

func scanStatRows(rows pgx.Rows) ([]StatRow, error) {
	defer rows.Close()
	out := []StatRow{}
	for rows.Next() {
		var r StatRow
		var pct, dpct pgtype.Numeric
		if err := rows.Scan(&r.Day, &r.Project, &r.Language, &r.Editor, &r.Branch,
			&r.Platform, &r.Machine, &r.Entity, &r.TotalSeconds, &pct, &dpct); err != nil {
			return nil, err
		}
		r.Pct = numToFloat(pct)
		r.DailyPct = numToFloat(dpct)
		out = append(out, r)
	}
	return out, rows.Err()
}

// trimSQL strips trailing whitespace and a trailing ';' so a query can be safely
// embedded as a subquery `( <inner> ) base`.
func trimSQL(q string) string {
	return strings.TrimRight(strings.TrimSpace(q), ";")
}
