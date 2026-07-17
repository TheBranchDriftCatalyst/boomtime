// activity.go holds the per-user stats/aggregation queries: activity, the daily
// rollup fast path, project stats, timeline, and time totals.
package db

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- Stats queries ----

// GetUserActivityRollup reads the pre-aggregated hb_rollup_daily (fast path for
// the Overview at the default 15-min limit). $1 user, $2 start, $3 end. Excludes
// the sender's hidden values and includes a Space scope's members for the axes
// the rollup stores (project, language, editor, platform, machine, category,
// plugin, branch). Callers must not use this path when a hide or Space rule is
// active on an axis the rollup lacks (entity today) — use the raw path.
// A rename needs NO rollup fallback: rename only RELABELS output columns (it
// never removes rows), and the rollup's OUTPUT columns are still project/
// language/editor/platform/machine — the finer stored axes (category/plugin/
// branch) are used only for filtering, then collapsed back by a CTE GROUP BY.
// A rename on branch has no output column here (branch is 'Other' in the output),
// so it can't mis-display.
func (d *DB) GetUserActivityRollup(ctx context.Context, user string, start, end time.Time, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]StatRow, error) {
	query, args, next := applyScopes(qGetUserActivityRoll, rollupRangeAnchor,
		hs, ms, spaceRequested, rollupCols, []any{user, start, end}, 4)
	query, args = rs.regroupStatRows(query, next, args)
	var out []StatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanStatRows(rows)
		return
	})
	return out, err
}

// rollupRangeAnchor is the inner range-end clause of get_user_activity_rollup.sql.
const rollupRangeAnchor = "AND day <= $3::date"

// GetUserActivity runs get_user_activity.sql ($1 user, $2 start, $3 end, $4 limit),
// excluding ALL of the sender's hidden axis values (project, language, editor,
// plugin, machine, platform, branch, category) via appended `AND NOT (<col> =
// ANY($n))` predicates on the raw-heartbeats scan.
func (d *DB) GetUserActivity(ctx context.Context, user string, start, end time.Time, limit int64, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]StatRow, error) {
	// Hide exclusion + space scope are spliced into the inner WHERE (anchored on
	// the range-end clause) so filtered rows are dropped (by RAW value) before
	// aggregation.
	query, args, next := applyScopes(qGetUserActivity, activityRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{user, start, end, limit}, 5)
	// Rename remap re-groups the surviving rows by display value (merges A,B→M).
	query, args = rs.regroupStatRows(query, next, args)
	var out []StatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanStatRows(rows)
		return
	})
	return out, err
}

// activityRangeAnchor is the inner range-end clause of get_user_activity.sql; the
// hide exclusion is spliced in right after it. Kept as a constant so a change to
// the .sql that removes this line fails loudly (injectAfter returns unchanged and
// tests catch the missing exclusion).
const activityRangeAnchor = "AND time_sent <= $3"

func scanProjectStatRows(rows pgx.Rows) ([]ProjectStatRow, error) {
	defer rows.Close()
	out := []ProjectStatRow{}
	for rows.Next() {
		var r ProjectStatRow
		var pct, dpct pgtype.Numeric
		if err := rows.Scan(&r.Day, &r.Weekday, &r.Hour, &r.Language, &r.Entity,
			&r.Ty, &r.TotalSeconds, &pct, &dpct); err != nil {
			return nil, err
		}
		r.Pct = numToFloat(pct)
		r.DailyPct = numToFloat(dpct)
		out = append(out, r)
	}
	return out, rows.Err()
}

// projectStatsRangeAnchor is the inner range-end clause where the hide exclusion
// is spliced. It scans raw heartbeats, so all axes are available.
const projectStatsRangeAnchor = "AND time_sent <= $4"

// projectStatsMatchClause is the raw project filter in get_projects_stats.sql. The
// $2 param is now a DISPLAY name, so a project rename replaces this with a
// remap-then-match so a merged name selects all its source rows. Kept as a
// constant so a drift in the .sql fails loudly.
const projectStatsMatchClause = "AND project = $2"

// GetProjectStats runs get_projects_stats.sql ($1 user,$2 project,$3 start,$4 end,$5 limit).
// The incoming `project` is a DISPLAY name: when a project rename is active the
// raw `project = $2` filter is replaced with `remap(project) = $2` so a merged
// name aggregates all its source projects (and identity still works). Hidden axis
// values are excluded within the project; the output `language` axis is remapped.
func (d *DB) GetProjectStats(ctx context.Context, user, project string, start, end time.Time, limit int64, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]ProjectStatRow, error) {
	query, args, next := applyScopes(qGetProjectsStats, projectStatsRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{user, project, start, end, limit}, 6)
	// Match the display name against the remapped raw project — always splice a
	// case-insensitive comparison so "MyProject" also matches "myproject" rows;
	// when no rename is active, `remapExpr` returns the raw column unchanged.
	var expr string
	expr, args, next = rs.remapExpr("project", "project", "", next, args)
	query = strings.Replace(query, projectStatsMatchClause, "AND lower("+expr+") = lower($2)", 1)
	// Remap + case-fold the output language axis (project axis isn't an output
	// column here). The wrap always runs so pure case variants on language/entity
	// merge without needing a rename rule.
	query, args = rs.regroupProjectStatRows(query, next, args)
	var out []ProjectStatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanProjectStatRows(rows)
		return
	})
	return out, err
}

// timelineRangeAnchor is the inner range-end clause of get_timeline.sql (raw
// heartbeats scan, unqualified columns); the space inclusion is spliced after it.
const timelineRangeAnchor = "AND time_sent < $3"

// GetTimeline runs get_timeline.sql ($1 user,$2 start,$3 end,$4 limit). When a
// Space is requested it keeps only rows matching the Space's membership rules.
func (d *DB) GetTimeline(ctx context.Context, user string, start, end time.Time, limit int64, ms MemberSets, spaceRequested bool) ([]TimelineRow, error) {
	// Audit-adjacent surface: no hide exclusion (zero HiddenSets), space scope only.
	query, args, _ := applyScopes(qGetTimeline, timelineRangeAnchor,
		HiddenSets{}, ms, spaceRequested, rawHeartbeatCols, []any{user, start, end, limit}, 5)
	out := []TimelineRow{}
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r TimelineRow
			if err := rows.Scan(&r.Lang, &r.Project, &r.RangeStart, &r.RangeEnd); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// GetTotalTimeToday runs get_time_today.sql ($1 user).
// timeTodayRangeAnchor is the inner day-bound clause of get_time_today.sql; the
// hide exclusion is spliced after it (raw heartbeats scan, all axes available).
const timeTodayRangeAnchor = "time_sent < (current_date + interval '1' day)"

// GetTotalTimeToday returns today's attributed coding time, excluding the sender's
// hidden axis values (so the statusbar total matches the hidden dashboards).
func (d *DB) GetTotalTimeToday(ctx context.Context, user string, hs HiddenSets) (int64, error) {
	// Hide exclusion only (the statusbar total has no space scope).
	query, args, _ := applyScopes(qGetTimeToday, timeTodayRangeAnchor,
		hs, MemberSets{}, false, rawHeartbeatCols, []any{user}, 2)
	var total int64
	err := d.Pool.QueryRow(ctx, query, args...).Scan(&total)
	return total, err
}

// GetTotalActivityTime runs get_total_project_time.sql ($1 user,$2 days,$3 project).
func (d *DB) GetTotalActivityTime(ctx context.Context, user string, days int64, project string) (int64, error) {
	var total int64
	err := d.Pool.QueryRow(ctx, qGetTotalProject, user, days, project).Scan(&total)
	return total, err
}

// GetTotalTimeBetween runs get_time_between.sql for a set of (user,project,min,max)
// ranges. Returns results in ascending order (reverse of the DESC insert order),
// matching hakatime's Database.getTotalTimeBetween.
func (d *DB) GetTotalTimeBetween(ctx context.Context, users, projects []string, mins, maxs []time.Time) ([]int64, error) {
	rows, err := d.Pool.Query(ctx, qGetTimeBetween, users, projects, mins, maxs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
