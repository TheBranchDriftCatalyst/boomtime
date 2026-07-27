// backfill.go: DB layer for the git-history backfill flow (gaka-vh8).
//
// Two responsibilities:
//
//  1. Per-user BackfillConfig persistence (backfill_config table).
//     GetBackfillConfig returns the current row (creating a defaults row
//     lazily on first read); SetBackfillConfig upserts.
//
//  2. Session-window-safe batch heartbeat insert.
//     InsertBackfillBatch takes a set of (start, end, heartbeats) tuples
//     and, for each session, checks whether ANY real (source IS NULL)
//     heartbeat exists for the same username inside [start, end]. If
//     yes, the entire session is skipped ("overlap"); if no, every
//     heartbeat in the session is inserted with source=<tag>.
//
// The overlap check uses `source IS NULL` because that's how we mark
// real Wakatime data (see migration 00037): the `sender` column has a
// users(username) FK and can't hold a tag string, so we added a
// nullable `source` column and set it to a "backfill:*" tag on every
// row this code writes. A rerun of the CLI does NOT count its own prior
// backfill rows as "real" (they have non-NULL source), so double-writes
// are absorbed by the unique_heartbeats constraint rather than
// cascading-dropping the whole session.
//
// PreviewBackfillBatch is InsertBackfillBatch without the insert step —
// used by the /admin/backfill/jobs/:id/preview endpoint so the UI can
// show "5 sessions would be written, 2 would be skipped as overlap"
// before the CLI commits.

package db

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/jackc/pgx/v5"
)

// BackfillConfig is the per-user tunable set for the backfill flow.
// Persisted in the backfill_config table (see migration 00037).
//
// LangMap maps file extensions (without leading dot) to language
// strings; consumers merge it with the compiled default in the git
// package (see git.EstimatorConfig.languageFor). Empty map = fall back
// to the compiled defaults.
//
// SourceTag is the `source` value written to every heartbeat this
// user's backfill produces. Defaults to "backfill:git". Operators may
// set to something like "backfill:git:2026-Q3" if they want to
// distinguish runs, though the danger-zone delete works on the
// `backfill:*` prefix regardless.
type BackfillConfig struct {
	Username          string            `json:"username"`
	ClusterGapSec     int               `json:"clusterGapSec"`
	PreCommitLeadSec  int               `json:"preCommitLeadSec"`
	PostCommitTailSec int               `json:"postCommitTailSec"`
	HeartbeatRateSec  int               `json:"heartbeatRateSec"`
	AuthorEmails      []string          `json:"authorEmails"`
	SourceTag         string            `json:"sourceTag"`
	LangMap           map[string]string `json:"langMap"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// defaultBackfillConfig is what a fresh row looks like — matches the
// DEFAULT clauses in migration 00037. Kept in Go too so
// GetBackfillConfig can lazily emit a synthetic default without an
// INSERT when the user hasn't touched their settings yet.
func defaultBackfillConfig(username string) BackfillConfig {
	return BackfillConfig{
		Username:          username,
		ClusterGapSec:     1800,
		PreCommitLeadSec:  900,
		PostCommitTailSec: 300,
		HeartbeatRateSec:  120,
		AuthorEmails:      []string{},
		SourceTag:         "backfill:git",
		LangMap:           map[string]string{},
	}
}

// GetBackfillConfig returns the config row for `username` or a fresh
// default set (not persisted) when no row exists. The synthetic default
// path lets a first-load Settings > Admin > Backfill tab show the
// defaults immediately without an extra POST/round-trip.
func (d *DB) GetBackfillConfig(ctx context.Context, username string) (BackfillConfig, error) {
	row := d.Pool.QueryRow(ctx, `
SELECT username,
       cluster_gap_sec,
       pre_commit_lead_sec,
       post_commit_tail_sec,
       heartbeat_rate_sec,
       author_emails,
       source_tag,
       lang_map,
       updated_at
FROM backfill_config
WHERE username = $1`, username)
	var cfg BackfillConfig
	var langRaw []byte
	err := row.Scan(&cfg.Username,
		&cfg.ClusterGapSec, &cfg.PreCommitLeadSec, &cfg.PostCommitTailSec,
		&cfg.HeartbeatRateSec, &cfg.AuthorEmails, &cfg.SourceTag,
		&langRaw, &cfg.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return defaultBackfillConfig(username), nil
		}
		return BackfillConfig{}, err
	}
	if len(langRaw) > 0 {
		_ = json.Unmarshal(langRaw, &cfg.LangMap)
	}
	if cfg.LangMap == nil {
		cfg.LangMap = map[string]string{}
	}
	if cfg.AuthorEmails == nil {
		cfg.AuthorEmails = []string{}
	}
	return cfg, nil
}

// SetBackfillConfig upserts the config row. Clamps every numeric field
// into a safe band before writing so a malicious/typo PATCH can't push
// the CLI into a state that emits tens of thousands of heartbeats per
// commit (e.g. HeartbeatRateSec=0 → materialize forever).
func (d *DB) SetBackfillConfig(ctx context.Context, cfg BackfillConfig) error {
	cfg = clampBackfillConfig(cfg)
	langRaw, err := json.Marshal(cfg.LangMap)
	if err != nil {
		return err
	}
	// Explicit empty array => Postgres text[] empty (not NULL). The Go
	// nil slice would encode as NULL, which conflicts with the NOT NULL
	// constraint. Normalize here so the SetBackfillConfig contract
	// accepts both nil and [] for "no allowlist".
	emails := cfg.AuthorEmails
	if emails == nil {
		emails = []string{}
	}
	_, err = d.Pool.Exec(ctx, `
INSERT INTO backfill_config
(username, cluster_gap_sec, pre_commit_lead_sec, post_commit_tail_sec,
 heartbeat_rate_sec, author_emails, source_tag, lang_map, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (username) DO UPDATE SET
  cluster_gap_sec = EXCLUDED.cluster_gap_sec,
  pre_commit_lead_sec = EXCLUDED.pre_commit_lead_sec,
  post_commit_tail_sec = EXCLUDED.post_commit_tail_sec,
  heartbeat_rate_sec = EXCLUDED.heartbeat_rate_sec,
  author_emails = EXCLUDED.author_emails,
  source_tag = EXCLUDED.source_tag,
  lang_map = EXCLUDED.lang_map,
  updated_at = now()`,
		cfg.Username, cfg.ClusterGapSec, cfg.PreCommitLeadSec, cfg.PostCommitTailSec,
		cfg.HeartbeatRateSec, emails, cfg.SourceTag, langRaw)
	return err
}

// clampBackfillConfig caps every numeric field to a safe band.
//   - ClusterGapSec:      [60, 4*3600]  (1min ≤ gap ≤ 4h)
//   - PreCommitLeadSec:   [0, 3600]
//   - PostCommitTailSec:  [0, 3600]
//   - HeartbeatRateSec:   [30, 900]     (30s ≤ rate ≤ 15min)
//
// SourceTag: falls back to "backfill:git" if empty; is prefix-normalized
// to begin with "backfill:" so the danger-zone DELETE / partial index /
// overlap filter all work correctly.
func clampBackfillConfig(cfg BackfillConfig) BackfillConfig {
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	cfg.ClusterGapSec = clamp(cfg.ClusterGapSec, 60, 4*3600)
	cfg.PreCommitLeadSec = clamp(cfg.PreCommitLeadSec, 0, 3600)
	cfg.PostCommitTailSec = clamp(cfg.PostCommitTailSec, 0, 3600)
	cfg.HeartbeatRateSec = clamp(cfg.HeartbeatRateSec, 30, 900)
	if cfg.SourceTag == "" {
		cfg.SourceTag = "backfill:git"
	}
	// Enforce the backfill:* prefix. If an operator PATCHes to
	// something like "git-history" without the prefix, upstream danger-
	// zone paths would leak it into "any source" territory. Prepend
	// backfill: rather than reject so a well-meaning PATCH is repaired
	// rather than 400'd.
	if len(cfg.SourceTag) < len("backfill:") || cfg.SourceTag[:len("backfill:")] != "backfill:" {
		cfg.SourceTag = "backfill:" + cfg.SourceTag
	}
	return cfg
}

// BackfillBatch is one CLI POST — a set of sessions the CLI wants us to
// consider. Each session carries its start/end (used for the overlap
// check) and the fully-materialized heartbeat list (Materialize output).
//
// Username is the owner receiving the heartbeats; SourceTag is the
// value written into the `source` column of every inserted row. We pass
// both separately (rather than trusting the wire body) so the caller
// can bind them from server-side context (resolved-from-token owner,
// backfill_config.source_tag) and a hostile CLI can't try to write into
// another user's timeline or forge a different tag.
type BackfillBatch struct {
	Username  string
	SourceTag string
	Sessions  []BackfillSession
}

// BackfillSession is one atomic overlap-check + insert unit.
type BackfillSession struct {
	Start      time.Time
	End        time.Time
	Heartbeats []model.HeartbeatPayload
}

// BackfillResult reports per-session outcomes in input order, so the
// caller can render "session 3: kept 42 heartbeats" vs "session 4:
// overlap, dropped".
type BackfillResult struct {
	Sessions []BackfillSessionResult `json:"sessions"`
	// Aggregate rollup for convenience.
	AcceptedHeartbeats int `json:"acceptedHeartbeats"`
	SkippedHeartbeats  int `json:"skippedHeartbeats"`
}

// BackfillSessionResult is one row in BackfillResult.Sessions.
type BackfillSessionResult struct {
	Accepted int    `json:"accepted"`
	Skipped  int    `json:"skipped"`
	Reason   string `json:"reason,omitempty"` // "overlap" or "" when kept
}

// InsertBackfillBatch runs the overlap check + insert for every session
// in b. Sessions are processed sequentially and each session is one
// transaction: the overlap check + the batched insert + the project
// upserts all commit or roll back together, so a mid-run failure never
// leaves a half-inserted session behind.
//
// Overlap check: `EXISTS (SELECT 1 FROM heartbeats WHERE sender = $user
// AND source IS NULL AND time_sent BETWEEN start AND end)`. `source IS
// NULL` isolates real Wakatime data; prior backfill rows have the tag
// set and are excluded so a rerun doesn't cascade-skip itself.
func (d *DB) InsertBackfillBatch(ctx context.Context, b BackfillBatch) (BackfillResult, error) {
	res := BackfillResult{Sessions: make([]BackfillSessionResult, 0, len(b.Sessions))}
	for _, s := range b.Sessions {
		outcome, err := d.processBackfillSession(ctx, b.Username, b.SourceTag, s, true)
		if err != nil {
			return res, err
		}
		res.Sessions = append(res.Sessions, outcome)
		res.AcceptedHeartbeats += outcome.Accepted
		res.SkippedHeartbeats += outcome.Skipped
	}
	return res, nil
}

// PreviewBackfillBatch runs the overlap check but does NOT insert.
// Used by the /admin/backfill/jobs/:id/preview endpoint so an operator
// can see the accepted/skipped mix without committing.
func (d *DB) PreviewBackfillBatch(ctx context.Context, b BackfillBatch) (BackfillResult, error) {
	res := BackfillResult{Sessions: make([]BackfillSessionResult, 0, len(b.Sessions))}
	for _, s := range b.Sessions {
		outcome, err := d.processBackfillSession(ctx, b.Username, b.SourceTag, s, false)
		if err != nil {
			return res, err
		}
		res.Sessions = append(res.Sessions, outcome)
		res.AcceptedHeartbeats += outcome.Accepted
		res.SkippedHeartbeats += outcome.Skipped
	}
	return res, nil
}

// processBackfillSession runs the overlap check for one session, and
// (when insert=true) inserts if there's no overlap. On overlap the
// entire session's heartbeats are counted as Skipped/reason=overlap.
// The insert is done in a single transaction that also handles project
// upserts.
func (d *DB) processBackfillSession(ctx context.Context, username, sourceTag string, s BackfillSession, insert bool) (BackfillSessionResult, error) {
	overlap, err := d.sessionOverlaps(ctx, username, s.Start, s.End)
	if err != nil {
		return BackfillSessionResult{}, err
	}
	if overlap {
		return BackfillSessionResult{
			Skipped: len(s.Heartbeats),
			Reason:  "overlap",
		}, nil
	}
	if !insert || len(s.Heartbeats) == 0 {
		return BackfillSessionResult{Accepted: len(s.Heartbeats)}, nil
	}
	if err := d.insertBackfillHeartbeats(ctx, username, sourceTag, s.Heartbeats); err != nil {
		return BackfillSessionResult{}, err
	}
	return BackfillSessionResult{Accepted: len(s.Heartbeats)}, nil
}

// sessionOverlaps returns true if any real (source IS NULL) heartbeat
// exists for `username` inside [start, end] (inclusive on both ends —
// a real heartbeat landing exactly on our first tick still counts as
// overlap; the goal is "never double count", never "minimize skips").
func (d *DB) sessionOverlaps(ctx context.Context, username string, start, end time.Time) (bool, error) {
	var found bool
	err := d.Pool.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1 FROM heartbeats
  WHERE sender = $1
    AND source IS NULL
    AND time_sent BETWEEN $2 AND $3
  LIMIT 1
)`, username, start.UTC(), end.UTC()).Scan(&found)
	return found, err
}

// insertBackfillHeartbeats writes one session's worth of heartbeats
// with source=sourceTag. Mirrors SaveHeartbeats' shape (project upsert
// + heartbeat batched insert + gap/rollup refresh) but uses its OWN
// insert query that carries the `source` column — the base
// insert_heartbeat.sql predates this migration and doesn't reference
// the new column. Doing it via a dedicated path keeps SaveHeartbeats'
// hot ingest path untouched (no wire-shape change for real Wakatime
// heartbeats).
//
// Uses `sender = username` verbatim (the FE-facing rule is "backfill
// tags via `source`, not `sender`"); the heartbeat's own
// HeartbeatPayload.Sender is overwritten to match — a hostile CLI body
// can't steer sender past the admin-scoped username.
func (d *DB) insertBackfillHeartbeats(ctx context.Context, username, sourceTag string, hbs []model.HeartbeatPayload) error {
	if len(hbs) == 0 {
		return nil
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Force every heartbeat's sender to the resolved owner. Backfill
	// runs must never write into another user's timeline, and the CLI
	// isn't trusted with the owner mapping.
	uname := username
	for i := range hbs {
		hbs[i].Sender = &uname
	}

	// Project upsert (owner, project) — same shape as SaveHeartbeats.
	if err := insertProjectsBatch(ctx, tx, hbs); err != nil {
		return err
	}

	// Heartbeat insert: pipelined batch, ON CONFLICT ON CONSTRAINT
	// unique_heartbeats DO UPDATE SET source = COALESCE(EXCLUDED.source,
	// heartbeats.source) so a rerun does not clobber a previously-set
	// tag (defense in depth — the CLI keys on a stable tag, but a
	// future path could re-key). No RETURNING id needed; the caller
	// tracks per-session counts from the input length.
	var b pgx.Batch
	for _, hb := range hbs {
		var cursor *string
		if hb.Cursorpos != nil {
			s := strconv.FormatInt(*hb.Cursorpos, 10)
			cursor = &s
		}
		b.Queue(qInsertBackfillHeartbeat,
			hb.Editor, hb.Plugin, hb.Platform, hb.Machine, hb.Sender,
			hb.UserAgent, hb.Branch, hb.Category, cursor, hb.Dependencies,
			hb.Entity, hb.IsWrite, hb.Language, hb.Lineno, hb.FileLines,
			hb.Project, string(hb.Type), unixToTime(hb.TimeSent),
			hb.AIInputTokens, hb.AIOutputTokens, hb.AILineChanges,
			hb.HumanLineChanges, hb.AIPromptLength, hb.AISession,
			hb.AISubscriptionPlan,
			hb.WorkoutKind, hb.WorkoutDurationS, hb.WorkoutKcal,
			hb.WorkoutAvgHR, hb.WorkoutDistanceM,
			sourceTag,
		)
	}
	br := tx.SendBatch(ctx, &b)
	// Drain the results; a nonzero error from any single Exec fails the
	// whole batch. defer br.Close() so an early return still releases
	// the batch resources.
	defer br.Close()
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}

	// Gap/rollup refresh — same as SaveHeartbeats. Use the earliest
	// inserted timestamp to bound the recompute window.
	var minT time.Time
	for _, hb := range hbs {
		t := unixToTime(hb.TimeSent)
		if minT.IsZero() || t.Before(minT) {
			minT = t
		}
	}
	if err := recomputeGaps(ctx, tx, username, minT); err != nil {
		return err
	}
	if err := refreshRollup(ctx, tx, username, minT); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// qInsertBackfillHeartbeat is the backfill-specific variant of
// insert_heartbeat.sql — same 30 base columns plus the new `source`
// column at $31. Kept inline (rather than a queries/*.sql file) because
// it's the only caller and it must stay in lockstep with the base
// query's column order.
const qInsertBackfillHeartbeat = `
INSERT INTO heartbeats
(
    editor, plugin, platform, machine, sender,
    user_agent, branch, category, cursorpos, dependencies,
    entity, is_write, language, lineno, file_lines,
    project, ty, time_sent,
    ai_input_tokens, ai_output_tokens, ai_line_changes,
    human_line_changes, ai_prompt_length, ai_session, ai_subscription_plan,
    workout_kind, workout_duration_s, workout_kcal, workout_avg_hr, workout_distance_m,
    source
)
VALUES ( $1, $2, $3, $4, $5,
         $6, $7, $8, $9, $10,
         $11, $12, $13, CAST($14 AS INT), $15,
         $16, $17, $18,
         $19, $20, $21, $22, $23, $24, $25,
         $26, $27, $28, $29, $30,
         $31 )
ON CONFLICT ON CONSTRAINT unique_heartbeats
DO UPDATE SET source = COALESCE(heartbeats.source, EXCLUDED.source)`

// BackfillStats is the response shape for the Admin tab's header row.
//   - TotalRows: every heartbeats row for `username` whose source LIKE
//     'backfill:%' (i.e. any backfill run, historical or current).
//   - Sources:   count per exact `source` value ("backfill:git",
//                "backfill:git:2026-Q3", ...). Empty map = no rows.
//   - Oldest / Newest: bounds of the affected time_sent range. Nil when
//     TotalRows == 0.
type BackfillStats struct {
	TotalRows int            `json:"totalRows"`
	Sources   map[string]int `json:"sources"`
	Oldest    *time.Time     `json:"oldest,omitempty"`
	Newest    *time.Time     `json:"newest,omitempty"`
}

// BackfillStatsFor returns aggregate counts + source breakdown for a
// user's backfill rows. Uses the partial index
// heartbeats_backfill_source_idx from migration 00037 so counts are
// cheap even against a many-million-row heartbeats table.
func (d *DB) BackfillStatsFor(ctx context.Context, username string) (BackfillStats, error) {
	stats := BackfillStats{Sources: map[string]int{}}

	// Total + oldest + newest in one round trip.
	var total int
	var oldest, newest *time.Time
	err := d.Pool.QueryRow(ctx, `
SELECT count(*)::int,
       min(time_sent),
       max(time_sent)
FROM heartbeats
WHERE sender = $1 AND source LIKE 'backfill:%'`, username).Scan(&total, &oldest, &newest)
	if err != nil {
		return stats, err
	}
	stats.TotalRows = total
	stats.Oldest = oldest
	stats.Newest = newest

	if total == 0 {
		return stats, nil
	}

	rows, err := d.Pool.Query(ctx, `
SELECT source, count(*)::int
FROM heartbeats
WHERE sender = $1 AND source LIKE 'backfill:%'
GROUP BY source`, username)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return stats, err
		}
		stats.Sources[src] = n
	}
	return stats, rows.Err()
}

// DeleteBackfilledHeartbeats removes backfill-tagged rows for
// `username` whose source matches `sourceLike` (SQL LIKE pattern). The
// `source LIKE 'backfill:%'` floor is a defense-in-depth guard: even if
// a caller sneaks in `sourceLike = '%'`, the extra AND clause keeps the
// DELETE from reaching real Wakatime rows (which have source IS NULL
// and therefore fall out of every non-trivial LIKE).
//
// Returns the number of rows removed.
func (d *DB) DeleteBackfilledHeartbeats(ctx context.Context, username, sourceLike string) (int64, error) {
	if sourceLike == "" {
		sourceLike = "backfill:%"
	}
	tag, err := d.Pool.Exec(ctx, `
DELETE FROM heartbeats
WHERE sender = $1
  AND source LIKE $2
  AND source LIKE 'backfill:%'`, username, sourceLike)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
