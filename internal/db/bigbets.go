package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// bigBetRangeAnchor is the inner range-end clause shared by the big-bet queries
// (all scan raw heartbeats); the all-axis hide exclusion is spliced in after it.
const bigBetRangeAnchor = "AND time_sent <= $3"

// applyBigBetHides splices the all-axis exclusion after the range anchor. All
// big-bet queries scan raw heartbeats with unqualified columns, so every hide
// axis is available. args already hold $1..$4; the exclusion starts at $5.
func applyBigBetHides(query string, hs HiddenSets, args []any) (string, []any) {
	if !hs.AnyHidden() {
		return query, args
	}
	pred, argsWith, _ := exclusionPredicate(hs, rawHeartbeatCols, 5, args)
	return injectAfter(query, bigBetRangeAnchor, pred), argsWith
}

// CategoryDailyRow is one (day, category) coding-time bucket.
type CategoryDailyRow struct {
	Day          time.Time
	Category     string
	TotalSeconds int64
	Pct          float64
	DailyPct     float64
}

// GetCategoryDaily returns per-day-per-category time (excluding all hidden axis
// values), for folding into the Overview stats payload. $4 limit is the gap
// cutoff minutes. Note: a category hidden here disappears entirely (the whole
// category axis is excluded when that category is hidden).
func (d *DB) GetCategoryDaily(ctx context.Context, sender string, start, end time.Time, limit int64, hs HiddenSets) ([]CategoryDailyRow, error) {
	query := qGetCategoryDaily
	args := []any{sender, start, end, limit}
	query, args = applyBigBetHides(query, hs, args)
	var out []CategoryDailyRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r CategoryDailyRow
			var pct, dpct pgtype.Numeric
			if err := rows.Scan(&r.Day, &r.Category, &r.TotalSeconds, &pct, &dpct); err != nil {
				return err
			}
			r.Pct = numToFloat(pct)
			r.DailyPct = numToFloat(dpct)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// PunchcardCell is one day-of-week x hour-of-day intensity bucket (UTC).
type PunchcardCell struct {
	Dow     int   `json:"dow"`  // 0=Sunday .. 6=Saturday
	Hour    int   `json:"hour"` // 0..23
	Seconds int64 `json:"seconds"`
}

// GetPunchcard returns dow x hour coding-time cells (excluding all hidden axis values).
func (d *DB) GetPunchcard(ctx context.Context, sender string, start, end time.Time, limit int64, hs HiddenSets) ([]PunchcardCell, error) {
	query := qGetPunchcard
	args := []any{sender, start, end, limit}
	query, args = applyBigBetHides(query, hs, args)
	var out []PunchcardCell
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var c PunchcardCell
			if err := rows.Scan(&c.Dow, &c.Hour, &c.Seconds); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// SessionRow is one sessionized block: its start day (UTC) and total seconds.
type SessionRow struct {
	Day     time.Time
	Seconds int64
}

// GetSessions returns one row per session (excluding all hidden axis values). The
// gap cutoff that both bounds in-session time and defines a session break is
// limit*60 seconds.
func (d *DB) GetSessions(ctx context.Context, sender string, start, end time.Time, limit int64, hs HiddenSets) ([]SessionRow, error) {
	query := qGetSessions
	args := []any{sender, start, end, limit}
	query, args = applyBigBetHides(query, hs, args)
	var out []SessionRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var s SessionRow
			if err := rows.Scan(&s.Day, &s.Seconds); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, err
}

// MomentumRow is one (project, week-start) coding-time bucket.
type MomentumRow struct {
	Project   string
	WeekStart time.Time
	Seconds   int64
}

// GetMomentum returns per-project weekly time (excluding all hidden axis values).
// The Go shaper picks the top-N projects and gap-fills the week series.
func (d *DB) GetMomentum(ctx context.Context, sender string, start, end time.Time, limit int64, hs HiddenSets) ([]MomentumRow, error) {
	query := qGetMomentum
	args := []any{sender, start, end, limit}
	query, args = applyBigBetHides(query, hs, args)
	var out []MomentumRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var m MomentumRow
			if err := rows.Scan(&m.Project, &m.WeekStart, &m.Seconds); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}
