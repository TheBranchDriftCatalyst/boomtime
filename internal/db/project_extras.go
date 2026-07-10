package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// projectExtrasMatchClause is the raw project filter in both project-extras
// queries. Under a project rename it becomes remap(project) = $2 so a merged
// DISPLAY name aggregates all its source projects (like GetProjectStats).
const projectExtrasMatchClause = "AND project = $2"

// ProjectDailyExtra is one day's authoring/reading split and file breadth for a
// project (ty='file' only). Aligned to a day in the Go shaper.
type ProjectDailyExtra struct {
	Day              time.Time
	WriteSeconds     int64
	ReadSeconds      int64
	DistinctEntities int64
}

// ProjectBranchRow is one (day, branch) activity bucket for a project.
type ProjectBranchRow struct {
	Day          time.Time
	Branch       string
	TotalSeconds int64
	Pct          float64
	DailyPct     float64
}

// ProjectExtras bundles the extra per-project metrics fetched alongside the main
// ProjectStatRow set (authoring/reading, branch activity, breadth-vs-depth).
type ProjectExtras struct {
	Daily    []ProjectDailyExtra
	Branches []ProjectBranchRow
}

// GetProjectExtras runs the two supplementary scans for the Projects page viz:
// per-day write/read/distinct-entities (ty='file') and per-day-per-branch time.
// Owner-scoped; same range/limit semantics as GetProjectStats. The incoming
// `project` is a DISPLAY name: a project rename remap-matches it so a merged name
// aggregates all its source projects; a branch rename remaps the branches[] output.
func (d *DB) GetProjectExtras(ctx context.Context, user, project string, start, end time.Time, limit int64, rs RenameSets) (*ProjectExtras, error) {
	ex := &ProjectExtras{}

	// --- Daily authoring/reading/breadth (project-match remap only) ---
	dailyQuery := qGetProjDailyExtras
	dailyArgs := []any{user, project, start, end, limit}
	if rs.HasAxis("project") {
		var expr string
		expr, dailyArgs, _ = rs.remapExpr("project", "project", "", 6, dailyArgs)
		dailyQuery = strings.Replace(dailyQuery, projectExtrasMatchClause, "AND ("+expr+") = $2", 1)
	}
	if err := d.aggQuery(ctx, dailyQuery, dailyArgs, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r ProjectDailyExtra
			if err := rows.Scan(&r.Day, &r.WriteSeconds, &r.ReadSeconds, &r.DistinctEntities); err != nil {
				return err
			}
			ex.Daily = append(ex.Daily, r)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}

	// --- Per-day-per-branch (project-match remap + branch output remap) ---
	branchQuery := qGetProjBranchDaily
	branchArgs := []any{user, project, start, end, limit}
	next := 6
	if rs.HasAxis("project") {
		var expr string
		expr, branchArgs, next = rs.remapExpr("project", "project", "", next, branchArgs)
		branchQuery = strings.Replace(branchQuery, projectExtrasMatchClause, "AND ("+expr+") = $2", 1)
	}
	if rs.HasAxis("branch") {
		// Re-group (day, branch, total_seconds) by the remapped branch and recompute
		// the pct/daily_pct windows so merged branch counts combine.
		var bexpr string
		bexpr, branchArgs, next = rs.remapExpr("branch", "branch", "", next, branchArgs)
		branchQuery = fmt.Sprintf(`WITH regrouped AS (
    SELECT day, %s AS branch, CAST(SUM(total_seconds) AS int8) AS total_seconds
    FROM ( %s ) base
    GROUP BY day, %s
)
SELECT day, branch, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM regrouped`, bexpr, trimSQL(branchQuery), bexpr)
	}
	if err := d.aggQuery(ctx, branchQuery, branchArgs, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r ProjectBranchRow
			var pct, dpct pgtype.Numeric
			if err := rows.Scan(&r.Day, &r.Branch, &r.TotalSeconds, &pct, &dpct); err != nil {
				return err
			}
			r.Pct = numToFloat(pct)
			r.DailyPct = numToFloat(dpct)
			ex.Branches = append(ex.Branches, r)
		}
		return rows.Err()
	}); err != nil {
		return nil, err
	}

	return ex, nil
}
