// award_ledger.go: persistence layer for the label-award streak system
// (gaka-mwp-streaks). The evaluator itself stays JIT client-side; this
// file just records WHICH labels fired per period so the FE can render
// "3x NIGHT WATCH" streak badges.

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PeriodType names the cadence a label recurs on. Empty is not a valid
// stored period_type in award_ledger (only labels.period_default may be
// empty, meaning "use kind default"). Lifetime is documented for the
// FE + admin picker but is never persisted — lifetime labels don't
// generate ledger rows because there's no "streak" concept.
type PeriodType string

const (
	PeriodDaily    PeriodType = "daily"
	PeriodWeekly   PeriodType = "weekly"
	PeriodMonthly  PeriodType = "monthly"
	PeriodLifetime PeriodType = "lifetime"
	PeriodAuto     PeriodType = "" // sentinel: use the kind-based default
)

// KindDefaultPeriod returns the built-in period a label kind falls back
// to when its labels.period_default is empty. Must match the migration
// header comment (00044_award_ledger.sql) so the docs and code don't
// drift.
func KindDefaultPeriod(kind string) PeriodType {
	switch kind {
	case "tier":
		return PeriodLifetime
	case "tribe":
		return PeriodLifetime
	case "archetype":
		return PeriodWeekly
	case "meme":
		return PeriodWeekly
	case "patch":
		return PeriodDaily
	}
	// Unknown kind (future ext): default to weekly so we log SOMETHING
	// rather than silently drop. Callers should validate kinds elsewhere.
	return PeriodWeekly
}

// ResolvePeriod returns the effective period for a label — per-label
// override wins, else the kind default. Returns PeriodLifetime for
// labels that don't generate ledger rows.
func ResolvePeriod(kind, perLabel string) PeriodType {
	if perLabel != "" {
		return PeriodType(perLabel)
	}
	return KindDefaultPeriod(kind)
}

// PeriodBounds returns the [start, end) window a given `at` timestamp
// falls into, in the caller-supplied timezone. The timezone MUST come
// from the gaka-dg7 resolver (never assume UTC — a "day" streak in
// Pacific must not break on a UTC day-flip).
//
// - daily:   local midnight → next local midnight
// - weekly:  Monday 00:00 local → next Monday 00:00 local (ISO week)
// - monthly: first-of-month 00:00 local → first-of-next-month 00:00 local
// - lifetime: returns zero-values + false — callers skip logging
func PeriodBounds(pt PeriodType, at time.Time, tz *time.Location) (start, end time.Time, ok bool) {
	if tz == nil {
		tz = time.UTC
	}
	local := at.In(tz)
	switch pt {
	case PeriodDaily:
		start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
		end = start.Add(24 * time.Hour)
		return start, end, true
	case PeriodWeekly:
		// ISO week starts Monday. Weekday: Sun=0..Sat=6; convert to
		// Mon=0..Sun=6 for the subtraction.
		wd := int(local.Weekday()) - 1
		if wd < 0 {
			wd = 6
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
		start = day.AddDate(0, 0, -wd)
		end = start.AddDate(0, 0, 7)
		return start, end, true
	case PeriodMonthly:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, tz)
		end = start.AddDate(0, 1, 0)
		return start, end, true
	}
	// Lifetime / auto (unresolved) → don't log.
	return time.Time{}, time.Time{}, false
}

// AwardLogItem is one (label_id, period_type) tuple in a batch write.
// PeriodType is redundant with the label's resolved period but included
// on the wire so the client's picture of "what period is this" matches
// the server's write (avoids weird midnight-race edge cases where the
// server would bucket differently than the client displayed).
type AwardLogItem struct {
	LabelID    string
	PeriodType PeriodType
}

// LogAwards upserts one row per (username, label_id, period_start)
// derived from the caller-supplied tz. Called from the /awards/log
// handler after the FE evaluator fires — idempotent within a period
// so repeated visits don't affect streak math.
//
// Silently drops items whose PeriodType resolves to lifetime — those
// aren't ledger-eligible.
func (d *DB) LogAwards(ctx context.Context, username string, items []AwardLogItem, tz *time.Location, at time.Time) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	// Collect valid rows first so we can do a single Exec.
	type row struct {
		labelID    string
		periodType PeriodType
		start, end time.Time
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		start, end, ok := PeriodBounds(it.PeriodType, at, tz)
		if !ok {
			continue
		}
		rows = append(rows, row{it.LabelID, it.PeriodType, start, end})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(`
INSERT INTO award_ledger (username, label_id, period_type, period_start, period_end)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (username, label_id, period_start) DO NOTHING`,
			username, r.labelID, string(r.periodType), r.start, r.end)
	}
	br := d.Pool.SendBatch(ctx, batch)
	defer br.Close()
	written := 0
	for range rows {
		ct, err := br.Exec()
		if err != nil {
			return written, err
		}
		if ct.RowsAffected() > 0 {
			written++
		}
	}
	return written, nil
}

// LedgerRow is one persisted award record — the exact shape stored in
// the award_ledger table, plus the label kind joined from labels for
// FE grouping. Returned by the ledger-inspector endpoint.
type LedgerRow struct {
	LabelID     string    `json:"labelId"`
	LabelName   string    `json:"labelName"`
	Kind        string    `json:"kind"`
	PeriodType  string    `json:"periodType"`
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`
	LoggedAt    time.Time `json:"loggedAt"`
}

// ListAwardLedger returns ledger rows for one user, newest period first.
// If labelID is non-empty, only rows for that label are returned; else
// all labels for the user. Caps at `limit` (0 or negative → 500).
// Joins labels to attach the display name + kind — FE consumers use
// both for grouping and quick "what does this label represent" glance.
func (d *DB) ListAwardLedger(ctx context.Context, username, labelID string, limit int) ([]LedgerRow, error) {
	if limit <= 0 {
		limit = 500
	}
	args := []any{username}
	filter := ""
	if labelID != "" {
		filter = " AND al.label_id = $2"
		args = append(args, labelID)
	}
	args = append(args, limit)
	limitArg := "$" + fmt.Sprint(len(args))
	rows, err := d.Pool.Query(ctx, `
SELECT al.label_id,
       COALESCE(l.label, al.label_id) AS label_name,
       COALESCE(l.kind,  '') AS kind,
       al.period_type,
       al.period_start,
       al.period_end,
       al.logged_at
FROM award_ledger al
LEFT JOIN labels l ON l.id = al.label_id
WHERE al.username = $1`+filter+`
ORDER BY al.period_start DESC, al.label_id ASC
LIMIT `+limitArg, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LedgerRow, 0, 128)
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.LabelID, &r.LabelName, &r.Kind, &r.PeriodType, &r.PeriodStart, &r.PeriodEnd, &r.LoggedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LabelStreak reports one label's current streak length. StreakCount is
// the count of consecutive periods (walking back from and including the
// current period) that fired for this label. Zero = the label did NOT
// fire in the current period (no badge shown).
type LabelStreak struct {
	LabelID     string    `json:"labelId"`
	StreakCount int       `json:"streakCount"`
	LastPeriod  time.Time `json:"lastPeriod"`
}

// GetLabelStreaks returns the current streak length for every label the
// user has EVER hit (empty result = no history). Walks backward from
// the current period per (label, period_type) and counts consecutive
// periods until the first gap.
//
// Streak semantics:
//   - Award MUST have fired in the CURRENT period to count as an
//     "active" streak. If today is Wed and the label last fired on
//     Monday, streak is 0 (streak was broken by Tuesday's miss).
//   - Otherwise, count how far back we can walk without a gap.
//
// Timezone-aware via the caller-supplied tz.
func (d *DB) GetLabelStreaks(ctx context.Context, username string, tz *time.Location, at time.Time) ([]LabelStreak, error) {
	// One query: for each label_id, count consecutive period_starts
	// from the most recent, stopping when the gap between adjacent
	// rows exceeds the period step. Simpler than a CTE + LAG — do the
	// walk in Go against a flat "all rows for this user ordered by
	// (label_id, period_start DESC)" scan.
	rows, err := d.Pool.Query(ctx, `
SELECT label_id, period_type, period_start
FROM award_ledger
WHERE username = $1
ORDER BY label_id ASC, period_start DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type periodRow struct {
		periodType PeriodType
		start      time.Time
	}
	byLabel := map[string][]periodRow{}
	for rows.Next() {
		var lid, pt string
		var ps time.Time
		if err := rows.Scan(&lid, &pt, &ps); err != nil {
			return nil, err
		}
		byLabel[lid] = append(byLabel[lid], periodRow{PeriodType(pt), ps})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]LabelStreak, 0, len(byLabel))
	for labelID, prs := range byLabel {
		if len(prs) == 0 {
			continue
		}
		// Compute the CURRENT period for the label's period type — the
		// most recent row MUST match this exact start, else streak = 0.
		latest := prs[0]
		curStart, _, ok := PeriodBounds(latest.periodType, at, tz)
		if !ok {
			continue
		}
		if !prs[0].start.Equal(curStart) {
			// Not fired in the current period → no active streak.
			continue
		}
		// Walk backward counting consecutive periods.
		streak := 1
		prev := prs[0].start
		for i := 1; i < len(prs); i++ {
			expected := periodStepBackward(latest.periodType, prev, tz)
			if !prs[i].start.Equal(expected) {
				break // gap → stop
			}
			streak++
			prev = prs[i].start
		}
		out = append(out, LabelStreak{
			LabelID:     labelID,
			StreakCount: streak,
			LastPeriod:  prs[0].start,
		})
	}
	return out, nil
}

// periodStepBackward returns the period_start of the period IMMEDIATELY
// before `cur` for the given period type. Used by the streak walker to
// check "is the next-oldest row exactly one period earlier?".
func periodStepBackward(pt PeriodType, cur time.Time, tz *time.Location) time.Time {
	if tz == nil {
		tz = time.UTC
	}
	local := cur.In(tz)
	switch pt {
	case PeriodDaily:
		return local.AddDate(0, 0, -1)
	case PeriodWeekly:
		return local.AddDate(0, 0, -7)
	case PeriodMonthly:
		return local.AddDate(0, -1, 0)
	}
	return time.Time{}
}

// ErrUnknownPeriod signals that a caller passed a period value neither
// in the well-known set (daily/weekly/monthly/lifetime) nor empty. Used
// by the handler's validation.
var ErrUnknownPeriod = errors.New("unknown period type")

// ValidatePeriod returns nil for well-known values (including empty and
// lifetime) or ErrUnknownPeriod for anything else. Used by the admin
// endpoint that lets an operator override labels.period_default.
func ValidatePeriod(p string) error {
	switch PeriodType(p) {
	case PeriodAuto, PeriodDaily, PeriodWeekly, PeriodMonthly, PeriodLifetime:
		return nil
	}
	return ErrUnknownPeriod
}
