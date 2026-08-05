// ingest.go holds heartbeat ingestion and the derived data it maintains
// (gap_seconds, hb_rollup_daily), including derived-data health and resync.
package db

import (
	"context"
	"errors"
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
	// gaka-dg7: refreshRollup needs to read users.timezone before rebuilding
	// so the daily bucket is computed in the sender's TZ. Both pgxpool.Pool
	// and pgx.Tx satisfy this method.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ---- Heartbeats ----

// SaveHeartbeats runs the full ingest atomically: project upserts + heartbeat
// upserts + per-sender gap/rollup recompute all commit or roll back together.
// Insert phases use pgx.Batch (pipelined) so N heartbeats cost one round trip
// instead of N. Returns the assigned heartbeat ids in input order.
func (d *DB) SaveHeartbeats(ctx context.Context, hbs []model.HeartbeatPayload) ([]int64, error) {
	return d.saveHeartbeats(ctx, hbs, true /* maintain rollups */)
}

// SaveHeartbeatsRaw ingests heartbeats WITHOUT phase 3 (recomputeGaps +
// refreshRollup) — the cheap path for identities denied CapGenerateRollups
// (gaka-0oe.3). Phases 1+2 (project upsert + heartbeat insert) are identical to
// SaveHeartbeats, so time-window queries still see the raw rows; they just fall
// back to on-the-fly aggregation instead of hb_rollup_daily. Used by the ingest
// handler when BOOM_FEATURE_ROLLUP_SKIP is on and the caller can't generate
// rollups (e.g. a service/ingest-only tier). Gap/rollup can be backfilled later
// via a rollup-refresh if the user is upgraded.
func (d *DB) SaveHeartbeatsRaw(ctx context.Context, hbs []model.HeartbeatPayload) ([]int64, error) {
	return d.saveHeartbeats(ctx, hbs, false /* skip rollups */)
}

// saveHeartbeats is the shared implementation. rollup=true runs phase 3;
// rollup=false stops after the raw inserts. Keeping ONE body means the two
// public variants can never drift on the phase-1/2 insert logic.
func (d *DB) saveHeartbeats(ctx context.Context, hbs []model.HeartbeatPayload, rollup bool) ([]int64, error) {
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
	// silently disagree with what was ingested. SKIPPED for SaveHeartbeatsRaw
	// (rollup=false) — the cheap ingest path for rollup-denied tiers.
	if rollup {
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
			// gaka-1l9: AI-assistance fields ($19..$25). Nullable at every
			// layer — heartbeats from plugins that don't emit them bind NULL.
			hb.AIInputTokens, hb.AIOutputTokens, hb.AILineChanges,
			hb.HumanLineChanges, hb.AIPromptLength, hb.AISession,
			hb.AISubscriptionPlan,
			// Health/workout fields ($26..$30). Populated by the workouts ingest
			// path; NULL for regular editor heartbeats.
			hb.WorkoutKind, hb.WorkoutDurationS, hb.WorkoutKcal,
			hb.WorkoutAvgHR, hb.WorkoutDistanceM,
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
//
// gaka-dg7: the `day` column is now computed in the sender's user-local TZ
// (resolved 3-level: users.timezone > BOOM_DEFAULT_TIMEZONE > UTC) so the
// fast-path get_user_activity_rollup.sql serves user-local daily buckets
// automatically — no read-side AT TIME ZONE dance needed. The DELETE
// anchor is also shifted to that TZ so we bracket the correct affected days
// (a `since` timestamp at 06:00 UTC = the prior day in Pacific, so we must
// DELETE from the earlier day to avoid orphaning a partial bucket).
func refreshRollup(ctx context.Context, q execer, sender string, since time.Time) error {
	// Resolve the sender's tz from the users row. Empty column => UTC (the
	// operator-default fall-through happens in the handler layer for read
	// paths; on the ingest hot path we keep this in a single query to avoid
	// a per-batch handler round-trip). A row that doesn't exist yields
	// ErrNoRows here; we treat that as "unknown user, skip refresh" — the
	// SELECT in the INSERT would produce no rows anyway.
	var tz string
	err := q.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(timezone, ''), 'UTC') FROM users WHERE username = $1`,
		sender).Scan(&tz)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	// $2 (since) is a Go time.Time — pgx encodes it as a timestamptz. To get
	// the sender's local date from it we `AT TIME ZONE $3` (converts
	// timestamptz to naked wall clock in tz).
	// $2 (since) can be time.Time{} (zero) for a full rebuild; pgx encodes
	// zero as '0001-01-01 00:00:00Z' which converts safely under AT TIME
	// ZONE.
	if _, err := q.Exec(ctx,
		`DELETE FROM hb_rollup_daily WHERE sender = $1 AND day >= (($2::timestamptz AT TIME ZONE $3)::date)`,
		sender, since, tz); err != nil {
		return err
	}
	// Workouts contribute their authoritative workout_duration_s, bypassing
	// gap-inference entirely — a 45-minute run counts as 2700s regardless of
	// whether HR samples were dense enough to close gaps. Regular editor
	// heartbeats keep the gap-bounded time-spent semantics (gap<=900s).
	//
	// heartbeats.time_sent is `timestamp without time zone` stored as UTC
	// (unixToTime in ingest.go), so to get the local date we chain
	// `AT TIME ZONE 'UTC'` (mark as UTC -> timestamptz) then `AT TIME ZONE
	// $3` (project to local wall clock) then `::date` (snap to local day).
	// The WHERE lower bound is symmetric: local midnight -> timestamptz ->
	// naked UTC to compare against time_sent.
	// Insert now writes the <axis>_missing sentinel columns alongside the
	// COALESCE'd axis values (gaka-6ci). The flag is true iff EVERY row in
	// the group had NULL on that axis — bool_and works because within a
	// group, either they're all NULL (heartbeats from a null-language
	// browser session) or they all share the same literal value (which
	// obviously isn't NULL). Mixed groups (a literal 'Other' project
	// alongside a NULL project) are impossible under the GROUP BY because
	// COALESCE('Other', 'Other') buckets them the same — bool_and(IS NULL)
	// then correctly reads FALSE (some literal 'Other'), so the row
	// survives per-axis pie discriminators.
	_, err = q.Exec(ctx, `
INSERT INTO hb_rollup_daily (sender, day,
    project, language, editor, platform, machine, category, plugin, branch,
    total_seconds,
    project_missing, language_missing, editor_missing, platform_missing,
    machine_missing, category_missing, plugin_missing, branch_missing)
SELECT sender, (((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $3)::date),
    coalesce(project, 'Other'), coalesce(language, 'Other'), coalesce(editor, 'Other'),
    coalesce(platform, 'Other'), coalesce(machine, 'Other'),
    coalesce(category, 'Other'), coalesce(plugin, 'Other'), coalesce(branch, 'Other'),
    sum(CASE
        WHEN workout_duration_s IS NOT NULL THEN workout_duration_s
        WHEN gap_seconds <= 900 THEN gap_seconds
        ELSE 0
    END),
    bool_and(project IS NULL), bool_and(language IS NULL), bool_and(editor IS NULL),
    bool_and(platform IS NULL), bool_and(machine IS NULL),
    bool_and(category IS NULL), bool_and(plugin IS NULL), bool_and(branch IS NULL)
FROM heartbeats
WHERE sender = $1
  AND time_sent >= ((($2::timestamptz AT TIME ZONE $3)::date) AT TIME ZONE $3 AT TIME ZONE 'UTC')
GROUP BY sender, (((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $3)::date), coalesce(project, 'Other'), coalesce(language, 'Other'),
    coalesce(editor, 'Other'), coalesce(platform, 'Other'), coalesce(machine, 'Other'),
    coalesce(category, 'Other'), coalesce(plugin, 'Other'), coalesce(branch, 'Other')`, sender, since, tz)
	return err
}

// GetLastKnownContext returns the most recent REAL (non-null, non-placeholder)
// value of each context axis — project, language, branch — for `sender`, each
// resolved independently. A placeholder is any value matching `<<%>>` (see
// wakatime.IsLastPlaceholder); those are excluded so a stored template token
// can never seed another substitution. Any axis with no real value ever comes
// back nil.
//
// This is the DB seed for the ingest-time substitution pass (internal/ingest):
// it primes the running "last known" per axis before the batch's own
// forward-fill takes over. Three correlated single-row lookups — cheap, and
// only called when a batch actually carries a placeholder.
func (d *DB) GetLastKnownContext(ctx context.Context, sender string) (project, language, branch *string, err error) {
	err = d.Pool.QueryRow(ctx, `
SELECT
  (SELECT project  FROM heartbeats WHERE sender = $1 AND project  IS NOT NULL AND project  NOT LIKE '<<%>>' ORDER BY time_sent DESC LIMIT 1),
  (SELECT language FROM heartbeats WHERE sender = $1 AND language IS NOT NULL AND language NOT LIKE '<<%>>' ORDER BY time_sent DESC LIMIT 1),
  (SELECT branch   FROM heartbeats WHERE sender = $1 AND branch   IS NOT NULL AND branch   NOT LIKE '<<%>>' ORDER BY time_sent DESC LIMIT 1)
`, sender).Scan(&project, &language, &branch)
	if err != nil {
		return nil, nil, nil, err
	}
	return project, language, branch, nil
}

// LastContextBackfillResult reports what `backfill last-context` did (or, in
// dry-run, WOULD do). Per axis: `*Substituted` rows had a prior real value and
// were rewritten to it; `*Nulled` rows had no prior real value and had the
// literal placeholder dropped to NULL. AffectedSenders is every sender that
// held any placeholder row — the set whose rollups must be rebuilt afterward.
type LastContextBackfillResult struct {
	ProjectSubstituted  int64
	ProjectNulled       int64
	LanguageSubstituted int64
	LanguageNulled      int64
	BranchSubstituted   int64
	BranchNulled        int64
	AffectedSenders     []string
	DryRun              bool
}

// lastCtxAxes are the three context columns the backfill rewrites. Hardcoded
// (never user input) so it's safe to interpolate into the axis SQL below.
var lastCtxAxes = []string{"project", "language", "branch"}

// BackfillLastContext resolves stored `<<LAST_*>>` placeholder rows across the
// WHOLE heartbeats table (all senders), mirroring the ingest-time substitution
// for rows written before that shipped. For each axis independently, a
// placeholder row is rewritten to the sender's most recent real value at an
// earlier time_sent; a placeholder with no prior real value has the literal
// dropped to NULL (never left verbatim). All writes run in ONE transaction —
// either every axis is fixed or none is.
//
// dryRun reports the same per-axis counts (what WOULD change) and the affected
// senders without writing.
//
// The rollups are NOT rebuilt here — changing project/branch/language shifts
// hb_rollup_daily buckets, so the caller MUST refresh each AffectedSenders
// rollup afterward (RefreshRollup from epoch). The `backfill last-context`
// command does this; see cmd/boomtime/backfill_lastcontext.go.
func (d *DB) BackfillLastContext(ctx context.Context, dryRun bool) (LastContextBackfillResult, error) {
	res := LastContextBackfillResult{DryRun: dryRun}

	senders, err := d.lastCtxAffectedSenders(ctx)
	if err != nil {
		return res, err
	}
	res.AffectedSenders = senders

	if dryRun {
		for _, col := range lastCtxAxes {
			sub, nul, err := d.lastCtxDryRunCounts(ctx, col)
			if err != nil {
				return res, err
			}
			setLastCtxCount(&res, col, sub, nul)
		}
		return res, nil
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	for _, col := range lastCtxAxes {
		// Substitute BEFORE null-out: substitution rewrites the rows that have
		// a prior real value to a non-placeholder, so the subsequent null-out
		// (which matches `<<%>>`) only touches the leftover no-prior rows.
		sub, err := lastCtxSubstitute(ctx, tx, col)
		if err != nil {
			return res, err
		}
		nul, err := lastCtxNullOut(ctx, tx, col)
		if err != nil {
			return res, err
		}
		setLastCtxCount(&res, col, sub, nul)
	}

	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// lastCtxAffectedSenders lists every sender holding at least one placeholder
// row on any axis — the rollup-rebuild set.
func (d *DB) lastCtxAffectedSenders(ctx context.Context) ([]string, error) {
	rows, err := d.Pool.Query(ctx, `
SELECT DISTINCT sender FROM heartbeats
WHERE sender IS NOT NULL
  AND (project LIKE '<<%>>' OR branch LIKE '<<%>>' OR language LIKE '<<%>>')
ORDER BY sender`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// lastCtxDryRunCounts returns (substitutable, nullable) row counts for one axis
// without writing.
func (d *DB) lastCtxDryRunCounts(ctx context.Context, col string) (sub, nul int64, err error) {
	// %[1]s = column; %% => literal % for LIKE. Column is a constant from
	// lastCtxAxes, never user input — no injection surface.
	q := `
SELECT
  count(*) FILTER (WHERE ` + lastCtxPriorExists(col) + `),
  count(*) FILTER (WHERE NOT (` + lastCtxPriorExists(col) + `))
FROM heartbeats h
WHERE h.` + col + ` LIKE '<<%>>'`
	err = d.Pool.QueryRow(ctx, q).Scan(&sub, &nul)
	return sub, nul, err
}

// lastCtxPriorExists is the correlated-subquery predicate "sender has an
// earlier real value on this axis". Shared by the count and substitute SQL so
// they can never diverge.
func lastCtxPriorExists(col string) string {
	return `EXISTS (SELECT 1 FROM heartbeats h2
		WHERE h2.sender = h.sender AND h2.time_sent < h.time_sent
		  AND h2.` + col + ` IS NOT NULL AND h2.` + col + ` NOT LIKE '<<%>>')`
}

// lastCtxSubstitute rewrites placeholder rows that HAVE a prior real value to
// that value. Returns rows affected.
func lastCtxSubstitute(ctx context.Context, tx pgx.Tx, col string) (int64, error) {
	q := `
UPDATE heartbeats h
SET ` + col + ` = (
    SELECT h2.` + col + ` FROM heartbeats h2
    WHERE h2.sender = h.sender AND h2.time_sent < h.time_sent
      AND h2.` + col + ` IS NOT NULL AND h2.` + col + ` NOT LIKE '<<%>>'
    ORDER BY h2.time_sent DESC LIMIT 1)
WHERE h.` + col + ` LIKE '<<%>>'
  AND ` + lastCtxPriorExists(col)
	tag, err := tx.Exec(ctx, q)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// lastCtxNullOut drops any remaining literal placeholder (no prior real value)
// to NULL. Returns rows affected.
func lastCtxNullOut(ctx context.Context, tx pgx.Tx, col string) (int64, error) {
	tag, err := tx.Exec(ctx, `UPDATE heartbeats SET `+col+` = NULL WHERE `+col+` LIKE '<<%>>'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func setLastCtxCount(res *LastContextBackfillResult, col string, sub, nul int64) {
	switch col {
	case "project":
		res.ProjectSubstituted, res.ProjectNulled = sub, nul
	case "language":
		res.LanguageSubstituted, res.LanguageNulled = sub, nul
	case "branch":
		res.BranchSubstituted, res.BranchNulled = sub, nul
	}
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
