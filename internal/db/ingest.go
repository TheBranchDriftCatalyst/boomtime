// ingest.go holds heartbeat ingestion and the derived data it maintains
// (gap_seconds, hb_rollup_daily), including derived-data health and resync.
package db

import (
	"context"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// execer is the shared surface of pgxpool.Pool and pgx.Tx that RecomputeGaps
// and RefreshRollup need — lets the same helpers run standalone or inside the
// SaveHeartbeats transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ---- Heartbeats ----

// SaveHeartbeats runs the full ingest atomically: project upserts + heartbeat
// upserts + per-sender gap/rollup recompute all commit or roll back together.
// Insert phases use pgx.Batch (pipelined) so N heartbeats cost one round trip
// instead of N. Returns the assigned heartbeat ids in input order.
func (d *DB) SaveHeartbeats(ctx context.Context, hbs []model.HeartbeatPayload) ([]int64, error) {
	if len(hbs) == 0 {
		return []int64{}, nil
	}

	// Ingest stores RAW values. Rename rules are applied at query time only (a
	// non-destructive, reversible remap), so heartbeats keep their original label
	// values forever — no canonicalization here.

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Phase 1: batched (owner, project) upserts. One round trip for all unique
	// pairs, even if the batch touches thousands of new projects.
	if err := insertProjectsBatch(ctx, tx, hbs); err != nil {
		return nil, err
	}

	// Phase 2: batched heartbeat upserts; RETURNING id preserves input order.
	ids, err := insertHeartbeatsBatch(ctx, tx, hbs)
	if err != nil {
		return nil, err
	}

	// Phase 3: maintain gap_seconds + hb_rollup_daily for each affected sender,
	// starting from the earliest inserted timestamp (so the next existing beat's
	// gap is also corrected on out-of-order inserts). Runs inside the same tx —
	// a failure here rolls back the raw inserts too, so derived data can never
	// silently disagree with what was ingested.
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
		if err := recomputeGaps(ctx, tx, sender, since); err != nil {
			return nil, err
		}
		if err := refreshRollup(ctx, tx, sender, since); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

// insertProjectsBatch pipelines project upserts for every unique (sender, project)
// pair in `hbs`. Sends one pgx.Batch so N unique pairs cost one round trip.
func insertProjectsBatch(ctx context.Context, tx pgx.Tx, hbs []model.HeartbeatPayload) error {
	seen := map[[2]string]struct{}{}
	var b pgx.Batch
	for _, hb := range hbs {
		if hb.Sender == nil || hb.Project == nil {
			continue
		}
		key := [2]string{*hb.Sender, *hb.Project}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		b.Queue(`INSERT INTO projects (owner, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			*hb.Sender, *hb.Project)
	}
	if b.Len() == 0 {
		return nil
	}
	br := tx.SendBatch(ctx, &b)
	defer br.Close()
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

// insertHeartbeatsBatch pipelines the heartbeat upserts and returns ids in input
// order. Order is preserved because pgx.Batch consumes results in enqueue order.
func insertHeartbeatsBatch(ctx context.Context, tx pgx.Tx, hbs []model.HeartbeatPayload) ([]int64, error) {
	var b pgx.Batch
	for _, hb := range hbs {
		// cursorpos is a TEXT column (hakatime encodes the int via `show`), so
		// send the decimal string, not an *int64 — pgx can't encode int into text.
		var cursor *string
		if hb.Cursorpos != nil {
			s := strconv.FormatInt(*hb.Cursorpos, 10)
			cursor = &s
		}
		b.Queue(qInsertHeartbeat,
			hb.Editor, hb.Plugin, hb.Platform, hb.Machine, hb.Sender,
			hb.UserAgent, hb.Branch, hb.Category, cursor, hb.Dependencies,
			hb.Entity, hb.IsWrite, hb.Language, hb.Lineno, hb.FileLines,
			hb.Project, string(hb.Type), unixToTime(hb.TimeSent),
		)
	}
	br := tx.SendBatch(ctx, &b)
	defer br.Close()
	ids := make([]int64, 0, len(hbs))
	for i := 0; i < len(hbs); i++ {
		var id int64
		if err := br.QueryRow().Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := br.Close(); err != nil {
		return nil, err
	}
	return ids, nil
}

// RecomputeGaps recomputes gap_seconds (seconds to the previous heartbeat for the
// same sender, in global time order) for that sender's rows at or after `since`.
// It anchors on the row immediately before `since` so the first affected row —
// and any existing beat that now follows a freshly inserted one — is correct.
func (d *DB) RecomputeGaps(ctx context.Context, sender string, since time.Time) error {
	return recomputeGaps(ctx, d.Pool, sender, since)
}

// recomputeGaps runs the gap SQL against any pool or in-flight tx.
func recomputeGaps(ctx context.Context, q execer, sender string, since time.Time) error {
	_, err := q.Exec(ctx, `
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
// rollup stays current; bounded to the touched days. Opens its own tx when
// called standalone; inside SaveHeartbeats the tx-scoped helper is used instead.
func (d *DB) RefreshRollup(ctx context.Context, sender string, since time.Time) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := refreshRollup(ctx, tx, sender, since); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// refreshRollup runs the DELETE+INSERT rollup rebuild against any pool or
// in-flight tx. Must run inside a tx to keep the DELETE and INSERT atomic.
func refreshRollup(ctx context.Context, q execer, sender string, since time.Time) error {
	if _, err := q.Exec(ctx,
		`DELETE FROM hb_rollup_daily WHERE sender = $1 AND day >= $2::date`, sender, since); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `
INSERT INTO hb_rollup_daily (sender, day, project, language, editor, platform, machine, category, plugin, branch, total_seconds)
SELECT sender, time_sent::date,
    coalesce(project, 'Other'), coalesce(language, 'Other'), coalesce(editor, 'Other'),
    coalesce(platform, 'Other'), coalesce(machine, 'Other'),
    coalesce(category, 'Other'), coalesce(plugin, 'Other'), coalesce(branch, 'Other'),
    sum(CASE WHEN gap_seconds <= 900 THEN gap_seconds ELSE 0 END)
FROM heartbeats
WHERE sender = $1 AND time_sent >= $2::date
GROUP BY sender, time_sent::date, coalesce(project, 'Other'), coalesce(language, 'Other'),
    coalesce(editor, 'Other'), coalesce(platform, 'Other'), coalesce(machine, 'Other'),
    coalesce(category, 'Other'), coalesce(plugin, 'Other'), coalesce(branch, 'Other')`, sender, since)
	return err
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
	// HeartbeatsIndexes lists each index on the heartbeats table with its
	// on-disk size, largest first. Surfaced on the Heartbeats page so the
	// operator can see the storage cost of the perf indexes added in
	// migrations 00019/00020 (project/branch/entity trigram + project
	// text_pattern_ops) alongside the older sender/time btrees.
	HeartbeatsIndexes []IndexSize `json:"heartbeatsIndexes"`
}

// IndexSize is one row of the heartbeats index inventory.
type IndexSize struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
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

	idx, err := d.heartbeatsIndexSizes(ctx)
	if err != nil {
		// Best-effort: an environment where pg_indexes is restricted shouldn't
		// blank the whole panel. Log-and-continue by returning what we have.
		return s, nil
	}
	s.HeartbeatsIndexes = idx
	return s, nil
}

// heartbeatsIndexSizes returns every index on the heartbeats table with its
// on-disk size, largest first. Used by GetDerivedStatus to surface the
// storage cost of each index — the trigram / text_pattern_ops indexes shipped
// for gaka-o4m are the biggest cost line items.
func (d *DB) heartbeatsIndexSizes(ctx context.Context) ([]IndexSize, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT indexname, pg_relation_size((schemaname || '.' || indexname)::regclass) AS bytes
		FROM pg_indexes
		WHERE tablename = 'heartbeats'
		ORDER BY bytes DESC, indexname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IndexSize{}
	for rows.Next() {
		var i IndexSize
		if err := rows.Scan(&i.Name, &i.Bytes); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
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
