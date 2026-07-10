package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

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
// Owner-scoped; same range/limit semantics as GetProjectStats.
func (d *DB) GetProjectExtras(ctx context.Context, user, project string, start, end time.Time, limit int64) (*ProjectExtras, error) {
	ex := &ProjectExtras{}

	if err := d.aggQuery(ctx, qGetProjDailyExtras, []any{user, project, start, end, limit}, func(rows pgx.Rows) error {
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

	if err := d.aggQuery(ctx, qGetProjBranchDaily, []any{user, project, start, end, limit}, func(rows pgx.Rows) error {
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
