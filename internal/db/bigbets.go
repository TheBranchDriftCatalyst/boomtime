package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// bigBetRangeAnchor is the inner range-end clause shared by the big-bet queries
// (all scan raw heartbeats with unqualified columns, so every hide axis is
// available); the hide exclusion + space scope are spliced in after it via
// applyScopes.
//
// gaka-dg7: args now hold $1..$5 ($5 = IANA tz name) so the predicates start
// at $6. The anchor itself is unchanged — it references only $3, and the
// exclusion is spliced immediately after the "<= $3" text.
const bigBetRangeAnchor = "AND time_sent <= $3"

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
//
// The category axis is case-folded: "Coding" and "coding" merge into one row;
// MODE() picks the most common raw casing as the display label. The wrap runs
// even with no rename rule active so pure case variants still collapse.
func (d *DB) GetCategoryDaily(ctx context.Context, sender string, start, end time.Time, limit int64, tz string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]CategoryDailyRow, error) {
	// gaka-dg7: $5 = IANA tz for the day bucket; predicates start at $6.
	query, args, next := applyScopes(qGetCategoryDaily, bigBetRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{sender, start, end, limit, tz}, 6)
	// Always wrap: re-group (day, lower(category)) picking a canonical display
	// casing GLOBALLY across all days (highest-total variant wins; alphabetical
	// tie-break). The rename remap is spliced into the SELECT (identity when no
	// rule exists). See remap.go caseFoldPickCanonicalCTE for the per-axis pick
	// pattern — this variant is inlined for a single axis (category).
	var expr string
	expr, args, next = rs.remapExpr("category", "category", "", next, args)
	query = fmt.Sprintf(`WITH by_variant AS (
    SELECT day, %s AS raw_val, lower(%s) AS lc, CAST(SUM(total_seconds) AS int8) AS total_seconds
    FROM ( %s ) base
    GROUP BY day, %s
),
canonical AS (
    SELECT DISTINCT ON (lc) lc, raw_val AS canonical
    FROM (SELECT lc, raw_val, SUM(total_seconds) AS s FROM by_variant GROUP BY lc, raw_val) t
    ORDER BY lc, s DESC, raw_val ASC
),
regrouped AS (
    SELECT b.day, c.canonical AS category, CAST(SUM(b.total_seconds) AS int8) AS total_seconds
    FROM by_variant b JOIN canonical c USING (lc)
    GROUP BY b.day, c.canonical
)
SELECT day, category, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif(sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM regrouped`, expr, expr, trimSQL(query), expr)
	_ = next
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

// GetPunchcard returns dow x hour coding-time cells (excluding all hidden axis
// values). No renamable axis in the output (dow/hour), so no rename remap applies.
func (d *DB) GetPunchcard(ctx context.Context, sender string, start, end time.Time, limit int64, tz string, hs HiddenSets, ms MemberSets, spaceRequested bool) ([]PunchcardCell, error) {
	// gaka-dg7: $5 = IANA tz for the dow/hour buckets. THIS is the query
	// where the badge-misfire bug lived — a Pacific user's 22:00 local
	// (06:00 UTC) never fired late-night-coder because the dow/hour bucket
	// was UTC. Now the bucket matches their clock, so the archetype
	// evaluator downstream (FE punchcard-condition) sees the right cells.
	query, args, _ := applyScopes(qGetPunchcard, bigBetRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{sender, start, end, limit, tz}, 6)
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
// limit*60 seconds. No renamable axis in the output (session_day), so no remap.
func (d *DB) GetSessions(ctx context.Context, sender string, start, end time.Time, limit int64, tz string, hs HiddenSets, ms MemberSets, spaceRequested bool) ([]SessionRow, error) {
	// gaka-dg7: $5 = IANA tz for the session_day bucket.
	query, args, _ := applyScopes(qGetSessions, bigBetRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{sender, start, end, limit, tz}, 6)
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
// The Go shaper picks the top-N projects and gap-fills the week series. A project
// rename re-groups the (project, week) rows by the remapped project (merges).
func (d *DB) GetMomentum(ctx context.Context, sender string, start, end time.Time, limit int64, tz string, hs HiddenSets, rs RenameSets, ms MemberSets, spaceRequested bool) ([]MomentumRow, error) {
	// gaka-dg7: $5 = IANA tz for the ISO Monday week-start bucket.
	query, args, next := applyScopes(qGetMomentum, bigBetRangeAnchor,
		hs, ms, spaceRequested, rawHeartbeatCols, []any{sender, start, end, limit, tz}, 6)
	// Always wrap: fold project casing and pick a canonical display GLOBALLY
	// across all weeks (highest-total variant wins; alphabetical ASC tie-break)
	// so a project doesn't surface as two rows when its case-mix changes across
	// weeks (gaka-5db). Runs even with no project rename so pure case variants
	// merge without a curation rule.
	var expr string
	expr, args, next = rs.remapExpr("project", "project", "", next, args)
	query = fmt.Sprintf(`WITH base AS ( %s ),
%s
SELECT cp.canonical AS project, week_start, CAST(SUM(base.total_seconds) AS int8) AS total_seconds
FROM base JOIN cproj cp ON cp.lc = lower(%s)
GROUP BY cp.canonical, week_start
ORDER BY project, week_start`,
		trimSQL(query),
		canonicalPickCTE("cproj", "base", expr),
		expr)
	_ = next
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
