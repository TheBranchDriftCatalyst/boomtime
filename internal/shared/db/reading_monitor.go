// reading_monitor.go: DB accessors for the PERSISTENT server-side Kindle
// reading-monitor (boom-books §5.1). Two concerns, both siloed on the user:
//
//   - the per-user toggle + toast mode (users.reading_monitor_enabled /
//     reading_monitor_mode) — what the admin endpoint reads/writes and what the
//     leader-singleton engine fans over (ListReadingMonitorUsers).
//   - the per-book monitor STATE (kindle_reading_monitor_state) the two-level
//     engine carries across ticks to detect advances + drive L1/L2/idle.
//
// See migrations/00072. Like reading_items / kindle_reading_positions this never
// writes into heartbeats/stats; it cascade-deletes with the user.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReadingMonitorMode is the toast verbosity for the persistent monitor.
const (
	ReadingMonitorModeDebounced = "debounced" // one toast per advancing book / status change
	ReadingMonitorModeVerbose   = "verbose"   // a toast on every observed advance
)

// GetReadingMonitorSettings returns a user's monitor toggle + mode. A missing
// user row surfaces the zero settings (disabled, debounced) with no error — the
// same forgiving contract as GetPublicProfile.
func (d *DB) GetReadingMonitorSettings(ctx context.Context, owner string) (enabled bool, mode string, err error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT reading_monitor_enabled, reading_monitor_mode FROM users WHERE username = $1`,
		owner)
	if serr := row.Scan(&enabled, &mode); serr != nil {
		if errors.Is(serr, pgx.ErrNoRows) {
			return false, ReadingMonitorModeDebounced, nil
		}
		return false, "", serr
	}
	return enabled, mode, nil
}

// SetReadingMonitorSettings does a PARTIAL update of a user's monitor settings:
// a nil enabled/mode leaves that column untouched, so the PUT endpoint can send
// either field alone. COALESCE keeps the other column at its current value. A
// missing user row surfaces as pgx.ErrNoRows so the handler can 404.
func (d *DB) SetReadingMonitorSettings(ctx context.Context, owner string, enabled *bool, mode *string) error {
	if owner == "" {
		return errors.New("SetReadingMonitorSettings: empty owner")
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET reading_monitor_enabled = COALESCE($2, reading_monitor_enabled),
		        reading_monitor_mode    = COALESCE($3, reading_monitor_mode)
		  WHERE username = $1`,
		owner, enabled, mode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetReadingMonitorCalibration returns a user's calibration-window expiry
// (users.reading_monitor_calibrating_until) — nil when not calibrating (the
// column is NULL) or the user row is missing. The engine tests now < this to
// decide whether to poll at the high-fidelity CalibrationInterval this pass.
func (d *DB) GetReadingMonitorCalibration(ctx context.Context, owner string) (*time.Time, error) {
	var until *time.Time
	row := d.Pool.QueryRow(ctx,
		`SELECT reading_monitor_calibrating_until FROM users WHERE username = $1`, owner)
	if err := row.Scan(&until); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return until, nil
}

// SetReadingMonitorCalibration sets (or, with a nil `until`, clears) a user's
// calibration-window expiry. StartReadingMonitorCalibration passes now+duration to
// begin a burst; the admin PUT passes nil to cancel one. A missing user row
// surfaces as pgx.ErrNoRows so the handler can 404.
func (d *DB) SetReadingMonitorCalibration(ctx context.Context, owner string, until *time.Time) error {
	if owner == "" {
		return errors.New("SetReadingMonitorCalibration: empty owner")
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users SET reading_monitor_calibrating_until = $2 WHERE username = $1`,
		owner, until)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListReadingMonitorUsers returns every user with the persistent monitor enabled
// — the fan-out set the leader-singleton engine sweeps each pass.
func (d *DB) ListReadingMonitorUsers(ctx context.Context) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT username FROM users WHERE reading_monitor_enabled = true ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// KindleMonitorState is the per-book state the two-level engine carries across
// ticks. LastAdvanceAt/LastPolledAt are nil until first set (a book the engine
// has never advanced/polled).
type KindleMonitorState struct {
	Owner         string
	ASIN          string
	LastLocation  int64
	LastAdvanceAt *time.Time
	LastPolledAt  *time.Time
	Active        bool
}

// ListKindleMonitorStates returns a user's whole per-book monitor state set,
// keyed by ASIN — the engine loads it once per pass and edits in memory.
func (d *DB) ListKindleMonitorStates(ctx context.Context, owner string) (map[string]KindleMonitorState, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, asin, last_location, last_advance_at, last_polled_at, active
		   FROM kindle_reading_monitor_state
		  WHERE owner = $1`,
		owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]KindleMonitorState{}
	for rows.Next() {
		var s KindleMonitorState
		if err := rows.Scan(&s.Owner, &s.ASIN, &s.LastLocation, &s.LastAdvanceAt, &s.LastPolledAt, &s.Active); err != nil {
			return nil, err
		}
		out[s.ASIN] = s
	}
	return out, rows.Err()
}

// UpsertKindleMonitorState persists one book's monitor state (insert or replace).
func (d *DB) UpsertKindleMonitorState(ctx context.Context, s KindleMonitorState) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO kindle_reading_monitor_state
		   (owner, asin, last_location, last_advance_at, last_polled_at, active, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6, now())
		 ON CONFLICT (owner, asin) DO UPDATE SET
		   last_location   = EXCLUDED.last_location,
		   last_advance_at = EXCLUDED.last_advance_at,
		   last_polled_at  = EXCLUDED.last_polled_at,
		   active          = EXCLUDED.active,
		   updated_at      = now()`,
		s.Owner, s.ASIN, s.LastLocation, s.LastAdvanceAt, s.LastPolledAt, s.Active)
	return err
}

// CountActiveKindleMonitorBooks counts a user's books currently in L2 (active) —
// the per-owner value the reading-monitor endpoint reports as activeBooks.
func (d *DB) CountActiveKindleMonitorBooks(ctx context.Context, owner string) (int, error) {
	var n int
	err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM kindle_reading_monitor_state WHERE owner = $1 AND active`, owner).Scan(&n)
	return n, err
}

// CountActiveKindleMonitorBooksGlobal counts active books across ALL users — the
// value the process-global boomtime_reading_monitor_active_books gauge is set to.
func (d *DB) CountActiveKindleMonitorBooksGlobal(ctx context.Context) (int, error) {
	var n int
	err := d.Pool.QueryRow(ctx,
		`SELECT count(*) FROM kindle_reading_monitor_state WHERE active`).Scan(&n)
	return n, err
}

// KindleAdvancePair is one observed intra-session advance: the wall-clock
// seconds since the same book's previous advance and the location delta. This is
// the raw signal the interval RECOMMENDATION consumes — the same data that feeds
// the advance_interval_seconds histogram, persisted so the endpoint can query it.
type KindleAdvancePair struct {
	IntervalSecs float64
	DLoc         int64
}

// InsertReadingMonitorAdvance appends one observed intra-session advance interval
// to the rolling window. Called by the engine at the same point it observes the
// advance_interval_seconds histogram (see monitor.go).
func (d *DB) InsertReadingMonitorAdvance(ctx context.Context, owner, source string, intervalSecs float64, dloc int64, at time.Time) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO kindle_reading_monitor_advances (owner, source, interval_secs, dloc, at)
		 VALUES ($1,$2,$3,$4,$5)`,
		owner, source, intervalSecs, dloc, at)
	return err
}

// ListRecentReadingMonitorAdvances returns a user's advance-interval samples at
// or after `since`, newest-first and capped at `limit` — the input the
// recommendation derives p50/p90 over (order-independent, so newest-first + cap is
// just a bound). `limit` is MonitorConfig.WindowCap (default 1000); a non-positive
// value falls back to 1000 so a caller can pass 0 for "the default bound".
func (d *DB) ListRecentReadingMonitorAdvances(ctx context.Context, owner string, since time.Time, limit int) ([]KindleAdvancePair, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT interval_secs, dloc
		   FROM kindle_reading_monitor_advances
		  WHERE owner = $1 AND at >= $2
		  ORDER BY at DESC
		  LIMIT $3`,
		owner, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindleAdvancePair
	for rows.Next() {
		var p KindleAdvancePair
		if err := rows.Scan(&p.IntervalSecs, &p.DLoc); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LastReadingMonitorPollAt returns the most recent time the engine polled ANY of
// a user's books (max last_polled_at) — the endpoint's lastPingAt. Nil when the
// engine has never polled for this user (monitor just enabled / no books).
func (d *DB) LastReadingMonitorPollAt(ctx context.Context, owner string) (*time.Time, error) {
	var at *time.Time
	err := d.Pool.QueryRow(ctx,
		`SELECT max(last_polled_at) FROM kindle_reading_monitor_state WHERE owner = $1`, owner).Scan(&at)
	if err != nil {
		return nil, err
	}
	return at, nil
}
