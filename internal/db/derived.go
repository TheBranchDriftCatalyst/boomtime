package db

import (
	"context"
	"time"
)

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
