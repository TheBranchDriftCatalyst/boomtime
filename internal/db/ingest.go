// ingest.go holds heartbeat ingestion and the derived data it maintains
// (gap_seconds, hb_rollup_daily), including derived-data health and resync.
package db

import (
	"context"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ---- Heartbeats ----

// SaveHeartbeats inserts unique projects then upserts heartbeats, returning ids.
func (d *DB) SaveHeartbeats(ctx context.Context, hbs []model.HeartbeatPayload) ([]int64, error) {
	if len(hbs) == 0 {
		return []int64{}, nil
	}

	// Ingest stores RAW values. Rename rules are applied at query time only (a
	// non-destructive, reversible remap), so heartbeats keep their original label
	// values forever — no canonicalization here.

	// Insert unique (owner, project) pairs first.
	seen := map[[2]string]struct{}{}
	for _, hb := range hbs {
		if hb.Sender != nil && hb.Project != nil {
			key := [2]string{*hb.Sender, *hb.Project}
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				if _, err := d.Pool.Exec(ctx,
					`INSERT INTO projects (owner, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
					*hb.Sender, *hb.Project); err != nil {
					return nil, err
				}
			}
		}
	}

	ids := make([]int64, 0, len(hbs))
	for _, hb := range hbs {
		var id int64
		// cursorpos is a TEXT column (hakatime encodes the int via `show`), so
		// send the decimal string, not an *int64 — pgx can't encode int into text.
		var cursor *string
		if hb.Cursorpos != nil {
			s := strconv.FormatInt(*hb.Cursorpos, 10)
			cursor = &s
		}
		row := d.Pool.QueryRow(ctx, qInsertHeartbeat,
			hb.Editor, hb.Plugin, hb.Platform, hb.Machine, hb.Sender,
			hb.UserAgent, hb.Branch, hb.Category, cursor, hb.Dependencies,
			hb.Entity, hb.IsWrite, hb.Language, hb.Lineno, hb.FileLines,
			hb.Project, string(hb.Type), unixToTime(hb.TimeSent),
		)
		if err := row.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	// Phase A: maintain the precomputed gap_seconds for each affected sender,
	// starting from the earliest inserted timestamp (so the next existing beat's
	// gap is also corrected on out-of-order inserts).
	minBySender := map[string]time.Time{}
	for _, hb := range hbs {
		if hb.Sender == nil {
			continue
		}
		t := unixToTime(hb.TimeSent)
		if cur, ok := minBySender[*hb.Sender]; !ok || t.Before(cur) {
			minBySender[*hb.Sender] = t
		}
	}
	for sender, since := range minBySender {
		if err := d.RecomputeGaps(ctx, sender, since); err != nil {
			return nil, err
		}
		if err := d.RefreshRollup(ctx, sender, since); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// RecomputeGaps recomputes gap_seconds (seconds to the previous heartbeat for the
// same sender, in global time order) for that sender's rows at or after `since`.
// It anchors on the row immediately before `since` so the first affected row —
// and any existing beat that now follows a freshly inserted one — is correct.
func (d *DB) RecomputeGaps(ctx context.Context, sender string, since time.Time) error {
	_, err := d.Pool.Exec(ctx, `
WITH anchor AS (
    SELECT COALESCE(max(time_sent), '-infinity'::timestamptz) AS t
    FROM heartbeats WHERE sender = $1 AND time_sent < $2
),
seq AS (
    SELECT h.id, h.time_sent,
        lag(h.time_sent) OVER (ORDER BY h.time_sent) AS prev
    FROM heartbeats h, anchor
    WHERE h.sender = $1 AND h.time_sent >= anchor.t
)
UPDATE heartbeats h
SET gap_seconds = CASE
        WHEN seq.prev IS NULL THEN NULL
        ELSE EXTRACT(EPOCH FROM (seq.time_sent - seq.prev))::int
    END
FROM seq
WHERE h.id = seq.id AND h.time_sent >= $2`, sender, since)
	return err
}

// RefreshRollup recomputes hb_rollup_daily for a sender's affected days (>= the
// date of `since`) from the raw heartbeats. Called after each ingest batch so the
// rollup stays current; bounded to the touched days.
func (d *DB) RefreshRollup(ctx context.Context, sender string, since time.Time) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM hb_rollup_daily WHERE sender = $1 AND day >= $2::date`, sender, since); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO hb_rollup_daily (sender, day, project, language, editor, platform, machine, total_seconds)
SELECT sender, time_sent::date,
    coalesce(project, 'Other'), coalesce(language, 'Other'), coalesce(editor, 'Other'),
    coalesce(platform, 'Other'), coalesce(machine, 'Other'),
    sum(CASE WHEN gap_seconds <= 900 THEN gap_seconds ELSE 0 END)
FROM heartbeats
WHERE sender = $1 AND time_sent >= $2::date
GROUP BY sender, time_sent::date, coalesce(project, 'Other'), coalesce(language, 'Other'),
    coalesce(editor, 'Other'), coalesce(platform, 'Other'), coalesce(machine, 'Other')`, sender, since); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func unixToTime(sec float64) time.Time {
	s := int64(sec)
	ns := int64((sec - float64(s)) * 1e9)
	return time.Unix(s, ns).UTC()
}

// DerivedStatus reports the health of the derived/precomputed data for a user:
// the gap_seconds column and the hb_rollup_daily rollup, plus whether they are in
// sync with the raw heartbeats.
type DerivedStatus struct {
	Heartbeats      int64 `json:"heartbeats"`
	GapPopulated    int64 `json:"gapPopulated"`
	GapMissing      int64 `json:"gapMissing"`
	RollupRows      int64 `json:"rollupRows"`
	RollupSeconds   int64 `json:"rollupSeconds"`
	RawSeconds      int64 `json:"rawSeconds"`
	InSync          bool  `json:"inSync"`
	HeartbeatsBytes int64 `json:"heartbeatsBytes"` // heartbeats table incl. indexes/toast
	RollupBytes     int64 `json:"rollupBytes"`     // hb_rollup_daily table incl. indexes
	DBBytes         int64 `json:"dbBytes"`         // whole database on disk
}

// GetDerivedStatus computes the derived-data health for a sender.
func (d *DB) GetDerivedStatus(ctx context.Context, sender string) (DerivedStatus, error) {
	var s DerivedStatus
	err := d.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM heartbeats WHERE sender = $1),
		  (SELECT count(gap_seconds) FROM heartbeats WHERE sender = $1),
		  (SELECT count(*) - count(gap_seconds) FROM heartbeats WHERE sender = $1),
		  (SELECT count(*) FROM hb_rollup_daily WHERE sender = $1),
		  (SELECT coalesce(sum(total_seconds), 0) FROM hb_rollup_daily WHERE sender = $1),
		  (SELECT coalesce(sum(CASE WHEN gap_seconds <= 900 THEN gap_seconds ELSE 0 END), 0) FROM heartbeats WHERE sender = $1),
		  pg_total_relation_size('heartbeats'),
		  pg_total_relation_size('hb_rollup_daily'),
		  pg_database_size(current_database())
	`, sender).Scan(&s.Heartbeats, &s.GapPopulated, &s.GapMissing, &s.RollupRows, &s.RollupSeconds, &s.RawSeconds,
		&s.HeartbeatsBytes, &s.RollupBytes, &s.DBBytes)
	if err != nil {
		return s, err
	}
	// In sync when the rollup total equals the raw total and at most one heartbeat
	// (the sender's first beat) legitimately lacks a gap.
	s.InSync = s.RollupSeconds == s.RawSeconds && s.GapMissing <= 1
	return s, nil
}

// ResyncDerived fully rebuilds gap_seconds and the rollup for a sender from the
// raw heartbeats (recomputes from the beginning of time).
func (d *DB) ResyncDerived(ctx context.Context, sender string) error {
	epoch := time.Unix(0, 0).UTC()
	if err := d.RecomputeGaps(ctx, sender, epoch); err != nil {
		return err
	}
	return d.RefreshRollup(ctx, sender, epoch)
}
