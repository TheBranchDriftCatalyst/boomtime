package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// statsWorkMem is applied via `SET LOCAL work_mem` to the heavy aggregation
// queries so their sorts stay in RAM instead of spilling to disk (on ~438k rows
// the disk spill roughly doubled latency). It is transaction-scoped, so it never
// leaks to other pooled connections. Tunable via HAKA_STATS_WORK_MEM (e.g. 128MB).
var statsWorkMem = "256MB"

var workMemPattern = regexp.MustCompile(`^[0-9]+(kB|MB|GB)$`)

func init() {
	if v := os.Getenv("HAKA_STATS_WORK_MEM"); workMemPattern.MatchString(v) {
		statsWorkMem = v
	}
}

// aggQuery runs a read-only aggregation query inside a transaction with an
// elevated work_mem, handing the rows to scan (which must consume/close them).
// The SET LOCAL is discarded by the read-only rollback.
func (d *DB) aggQuery(ctx context.Context, sql string, args []any, scan func(pgx.Rows) error) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL work_mem = '"+statsWorkMem+"'"); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	return scan(rows)
}

// numToFloat converts a scanned pgtype.Numeric to float64 (numeric(13,12) percentages).
func numToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// ---- Users & tokens ----

// GetUserByName returns the stored user credentials or (nil,nil) if absent.
func (d *DB) GetUserByName(ctx context.Context, name string) (*StoredUser, error) {
	row := d.Pool.QueryRow(ctx, `SELECT username, hashed_password, salt_used FROM users WHERE username = $1`, name)
	var u StoredUser
	if err := row.Scan(&u.Username, &u.HashedPassword, &u.SaltUsed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// InsertUser inserts a new user; returns false if the username already exists.
func (d *DB) InsertUser(ctx context.Context, u StoredUser) (bool, error) {
	existing, err := d.GetUserByName(ctx, u.Username)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return false, nil
	}
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, $2, $3)`,
		u.Username, u.HashedPassword, u.SaltUsed)
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetUserByToken returns the owner of an API token, honoring token_expiry
// (CLI/api tokens have null expiry and never expire), and bumps last_usage.
func (d *DB) GetUserByToken(ctx context.Context, token string) (string, bool, error) {
	row := d.Pool.QueryRow(ctx, `
		SELECT owner FROM auth_tokens
		WHERE  token = $1
		AND    COALESCE(token_expiry, (NOW() + interval '1 hours')::timestamp without time zone) > $2`,
		token, time.Now().UTC())
	var owner string
	if err := row.Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	// Bump last usage (best-effort; ignore error impact on the read path).
	_, _ = d.Pool.Exec(ctx, `UPDATE auth_tokens SET last_usage = now()::timestamp WHERE token = $1`, token)
	return owner, true, nil
}

// GetUserByRefreshToken returns the owner of a non-expired refresh token.
func (d *DB) GetUserByRefreshToken(ctx context.Context, token string) (string, bool, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT owner FROM refresh_tokens WHERE refresh_token = $1 AND token_expiry > $2`,
		token, time.Now().UTC())
	var owner string
	if err := row.Scan(&owner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return owner, true, nil
}

// CreateAccessTokens inserts an access token (30 min) and a refresh token
// (expiry hours) then deletes any expired tokens for the owner.
func (d *DB) CreateAccessTokens(ctx context.Context, td TokenData, expiryHours int64) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`INSERT INTO auth_tokens(owner, token, token_expiry) VALUES ($1, $2, NOW() + interval '30 minutes')`,
		td.Owner, td.Token); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO refresh_tokens(owner, refresh_token, token_expiry) VALUES ($1, $2, NOW() + interval '1' hour * $3)`,
		td.Owner, td.RefreshToken, expiryHours); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM auth_tokens WHERE owner = $1 AND token_expiry < NOW()`, td.Owner); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE owner = $1 AND token_expiry < NOW()`, td.Owner); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InsertAPIToken stores a base64(uuid) token with a null expiry (never expires).
func (d *DB) InsertAPIToken(ctx context.Context, owner, token string) error {
	_, err := d.Pool.Exec(ctx, `INSERT INTO auth_tokens(owner, token) VALUES ($1, $2)`, owner, token)
	return err
}

// DeleteTokens removes an auth token and refresh token, returning rows affected.
func (d *DB) DeleteTokens(ctx context.Context, token, refreshToken string) (int64, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	t1, err := tx.Exec(ctx, `DELETE FROM auth_tokens WHERE token = $1`, token)
	if err != nil {
		return 0, err
	}
	t2, err := tx.Exec(ctx, `DELETE FROM refresh_tokens WHERE refresh_token = $1`, refreshToken)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return t1.RowsAffected() + t2.RowsAffected(), nil
}

// DeleteAuthToken deletes an API token by its value.
func (d *DB) DeleteAuthToken(ctx context.Context, token string) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE token = $1`, token)
	return err
}

// ListApiTokens returns the non-expiring API tokens for a user.
func (d *DB) ListApiTokens(ctx context.Context, owner string) ([]model.StoredApiToken, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT token, last_usage::timestamptz, token_name, token_description
		FROM auth_tokens
		WHERE owner = $1 AND token_expiry IS NULL`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.StoredApiToken{}
	for rows.Next() {
		var t model.StoredApiToken
		var lu *time.Time
		if err := rows.Scan(&t.ID, &lu, &t.Name, &t.Desc); err != nil {
			return nil, err
		}
		t.LastUsage = lu
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTokenMetadata renames a token (POST /auth/token).
func (d *DB) UpdateTokenMetadata(ctx context.Context, owner string, m model.TokenMetadata) error {
	_, err := d.Pool.Exec(ctx,
		`UPDATE auth_tokens SET token_name = $3 WHERE token = $1 AND owner = $2`,
		m.TokenID, owner, m.TokenName)
	return err
}

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

// RollupAxes are the hide axes the pre-aggregated hb_rollup_daily table can
// exclude (it only stores these columns). A hide on any axis outside this set
// (plugin/branch/category) requires the raw path — see HasHiddenOutside; the
// stats handler falls back accordingly.
var RollupAxes = map[string]bool{
	"project": true, "language": true, "editor": true, "platform": true, "machine": true,
}

// rollupCols maps the rollup-available hide axes to their columns.
var rollupCols = map[string]string{
	"project": "project", "language": "language", "editor": "editor",
	"platform": "platform", "machine": "machine",
}

// GetUserActivityRollup reads the pre-aggregated hb_rollup_daily (fast path for
// the Overview at the default 15-min limit). $1 user, $2 start, $3 end. Excludes
// the sender's hidden values for the axes the rollup stores (project, language,
// editor, platform, machine). Callers must not use this path when a hide is
// active on an axis the rollup lacks (plugin/branch/category) — use the raw path.
// A rename needs NO rollup fallback: rename only RELABELS output columns (it never
// removes rows), and the rollup's output columns are project/language/editor/
// platform/machine — exactly the axes it stores. A rename on plugin/branch/
// category has no output column here, so it can't mis-display; re-summing the
// pre-aggregated rows by the remapped value merges correctly.
func (d *DB) GetUserActivityRollup(ctx context.Context, user string, start, end time.Time, hs HiddenSets, rs RenameSets) ([]StatRow, error) {
	query := qGetUserActivityRoll
	args := []any{user, start, end}
	next := 4
	if hs.AnyHidden() {
		var pred string
		pred, args, next = exclusionPredicate(hs, rollupCols, next, args)
		query = injectAfter(query, rollupRangeAnchor, pred)
	}
	query, args = rs.regroupStatRows(query, next, args)
	var out []StatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanStatRows(rows)
		return
	})
	return out, err
}

// rollupRangeAnchor is the inner range-end clause of get_user_activity_rollup.sql.
const rollupRangeAnchor = "AND day <= $3::date"

// injectAfter splices addition into query immediately after the first occurrence
// of anchor. If anchor is absent it returns query unchanged (so a drifted .sql is
// caught by the exclusion tests rather than producing broken SQL).
func injectAfter(query, anchor, addition string) string {
	if addition == "" {
		return query
	}
	idx := strings.Index(query, anchor)
	if idx < 0 {
		return query
	}
	pos := idx + len(anchor)
	return query[:pos] + addition + query[pos:]
}

func unixToTime(sec float64) time.Time {
	s := int64(sec)
	ns := int64((sec - float64(s)) * 1e9)
	return time.Unix(s, ns).UTC()
}

// ---- Stats queries ----

func scanStatRows(rows pgx.Rows) ([]StatRow, error) {
	defer rows.Close()
	out := []StatRow{}
	for rows.Next() {
		var r StatRow
		var pct, dpct pgtype.Numeric
		if err := rows.Scan(&r.Day, &r.Project, &r.Language, &r.Editor, &r.Branch,
			&r.Platform, &r.Machine, &r.Entity, &r.TotalSeconds, &pct, &dpct); err != nil {
			return nil, err
		}
		r.Pct = numToFloat(pct)
		r.DailyPct = numToFloat(dpct)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetUserActivity runs get_user_activity.sql ($1 user, $2 start, $3 end, $4 limit),
// excluding ALL of the sender's hidden axis values (project, language, editor,
// plugin, machine, platform, branch, category) via appended `AND NOT (<col> =
// ANY($n))` predicates on the raw-heartbeats scan.
func (d *DB) GetUserActivity(ctx context.Context, user string, start, end time.Time, limit int64, hs HiddenSets, rs RenameSets) ([]StatRow, error) {
	query := qGetUserActivity
	args := []any{user, start, end, limit}
	next := 5
	if hs.AnyHidden() {
		// Append the exclusion to the inner WHERE (anchored on the range-end
		// clause) so hidden rows are dropped (by RAW value) before aggregation.
		var pred string
		pred, args, next = exclusionPredicate(hs, rawHeartbeatCols, next, args)
		query = injectAfter(query, activityRangeAnchor, pred)
	}
	// Rename remap re-groups the surviving rows by display value (merges A,B→M).
	query, args = rs.regroupStatRows(query, next, args)
	var out []StatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanStatRows(rows)
		return
	})
	return out, err
}

// activityRangeAnchor is the inner range-end clause of get_user_activity.sql; the
// hide exclusion is spliced in right after it. Kept as a constant so a change to
// the .sql that removes this line fails loudly (injectAfter returns unchanged and
// tests catch the missing exclusion).
const activityRangeAnchor = "AND time_sent <= $3"

// GetUserActivityByTag runs get_user_activity_by_tags.sql ($1 user,$2 start,$3 end,$4 tag,$5 limit).
// userActivityTagRangeAnchor is the inner range-end clause of
// get_user_activity_by_tags.sql (raw heartbeats scan; $1..$5, exclusion at $6).
const userActivityTagRangeAnchor = "AND time_sent <= $3"

func (d *DB) GetUserActivityByTag(ctx context.Context, user string, start, end time.Time, tag string, limit int64, hs HiddenSets, rs RenameSets) ([]StatRow, error) {
	query := qGetUserActivityTag
	args := []any{user, start, end, tag, limit}
	next := 6
	if hs.AnyHidden() {
		var pred string
		pred, args, next = exclusionPredicate(hs, rawHeartbeatCols, next, args)
		query = injectAfter(query, userActivityTagRangeAnchor, pred)
	}
	query, args = rs.regroupStatRows(query, next, args)
	var out []StatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanStatRows(rows)
		return
	})
	return out, err
}

func scanProjectStatRows(rows pgx.Rows) ([]ProjectStatRow, error) {
	defer rows.Close()
	out := []ProjectStatRow{}
	for rows.Next() {
		var r ProjectStatRow
		var pct, dpct pgtype.Numeric
		if err := rows.Scan(&r.Day, &r.Weekday, &r.Hour, &r.Language, &r.Entity,
			&r.Ty, &r.TotalSeconds, &pct, &dpct); err != nil {
			return nil, err
		}
		r.Pct = numToFloat(pct)
		r.DailyPct = numToFloat(dpct)
		out = append(out, r)
	}
	return out, rows.Err()
}

// projectStatsRangeAnchor / tagStatsRangeAnchor are the inner range-end clauses
// where the hide exclusion is spliced. Both scan raw heartbeats, so all axes are
// available. tag-stats qualifies columns with `heartbeats.` (it joins tags).
const projectStatsRangeAnchor = "AND time_sent <= $4"
const tagStatsRangeAnchor = "AND heartbeats.time_sent <= $4"

// projectStatsMatchClause is the raw project filter in get_projects_stats.sql. The
// $2 param is now a DISPLAY name, so a project rename replaces this with a
// remap-then-match so a merged name selects all its source rows. Kept as a
// constant so a drift in the .sql fails loudly.
const projectStatsMatchClause = "AND project = $2"

var tagStatsCols = map[string]string{
	"project": "heartbeats.project", "language": "heartbeats.language",
	"editor": "heartbeats.editor", "plugin": "heartbeats.plugin",
	"machine": "heartbeats.machine", "platform": "heartbeats.platform",
	"branch": "heartbeats.branch", "category": "heartbeats.category",
}

// GetProjectStats runs get_projects_stats.sql ($1 user,$2 project,$3 start,$4 end,$5 limit).
// The incoming `project` is a DISPLAY name: when a project rename is active the
// raw `project = $2` filter is replaced with `remap(project) = $2` so a merged
// name aggregates all its source projects (and identity still works). Hidden axis
// values are excluded within the project; the output `language` axis is remapped.
func (d *DB) GetProjectStats(ctx context.Context, user, project string, start, end time.Time, limit int64, hs HiddenSets, rs RenameSets) ([]ProjectStatRow, error) {
	query := qGetProjectsStats
	args := []any{user, project, start, end, limit}
	next := 6
	if hs.AnyHidden() {
		var pred string
		pred, args, next = exclusionPredicate(hs, rawHeartbeatCols, next, args)
		query = injectAfter(query, projectStatsRangeAnchor, pred)
	}
	// Match the display name against the remapped raw project.
	if rs.HasAxis("project") {
		var expr string
		expr, args, next = rs.remapExpr("project", "project", "", next, args)
		query = strings.Replace(query, projectStatsMatchClause, "AND ("+expr+") = $2", 1)
	}
	// Remap the output language axis (project axis isn't an output column here).
	query, args = rs.regroupProjectStatRows(query, next, args)
	var out []ProjectStatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanProjectStatRows(rows)
		return
	})
	return out, err
}

// GetTagStats runs get_tag_stats.sql ($1 user,$2 tag,$3 start,$4 end,$5 limit),
// excluding the sender's hidden axis values within the tag's projects. Tag
// membership is a RAW-project fact (project_tags keys on the raw project name), so
// the join stays on raw projects — a project rename does NOT move tags. Only the
// output `language` axis is remapped for display.
func (d *DB) GetTagStats(ctx context.Context, user, tag string, start, end time.Time, limit int64, hs HiddenSets, rs RenameSets) ([]ProjectStatRow, error) {
	query := qGetTagStats
	args := []any{user, tag, start, end, limit}
	next := 6
	if hs.AnyHidden() {
		var pred string
		pred, args, next = exclusionPredicate(hs, tagStatsCols, next, args)
		query = injectAfter(query, tagStatsRangeAnchor, pred)
	}
	query, args = rs.regroupProjectStatRows(query, next, args)
	var out []ProjectStatRow
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) (e error) {
		out, e = scanProjectStatRows(rows)
		return
	})
	return out, err
}

// GetTimeline runs get_timeline.sql ($1 user,$2 start,$3 end,$4 limit).
func (d *DB) GetTimeline(ctx context.Context, user string, start, end time.Time, limit int64) ([]TimelineRow, error) {
	out := []TimelineRow{}
	err := d.aggQuery(ctx, qGetTimeline, []any{user, start, end, limit}, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r TimelineRow
			if err := rows.Scan(&r.Lang, &r.Project, &r.RangeStart, &r.RangeEnd); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// GetLeaderboards runs get_leaderboards.sql ($1 start,$2 end).
// leaderboardsRangeAnchor is the inner range-end clause of get_leaderboards.sql.
const leaderboardsRangeAnchor = "AND time_sent <= $2"

// GetLeaderboards aggregates coding time across ALL users. Both hide and rename
// apply to the REQUESTER's own rows only (multi-user safe: one user's curation
// must not alter other users' leaderboard contributions). Hide excludes with
// `AND NOT (sender = $req AND <col> = ANY($n))`; rename re-groups the requester's
// project/language via `CASE WHEN sender = $req AND col = ANY(..) THEN ..`.
func (d *DB) GetLeaderboards(ctx context.Context, start, end time.Time, requester string, hs HiddenSets, rs RenameSets) ([]LeaderboardRow, error) {
	query := qGetLeaderboards
	args := []any{start, end}

	// A single $req param is reused by both hide and rename when either is active.
	requesterArg := 0
	next := 3
	ensureRequester := func() {
		if requesterArg == 0 {
			args = append(args, requester)
			requesterArg = len(args)
			next = len(args) + 1
		}
	}

	if hs.AnyHidden() {
		ensureRequester()
		var pred string
		for _, axis := range hiddenAxes {
			vals := hs.Values(axis)
			col := rawHeartbeatCols[axis]
			if len(vals) == 0 || col == "" {
				continue
			}
			pred += fmt.Sprintf(" AND NOT (sender = $%d AND %s = ANY($%d))", requesterArg, col, next)
			args = append(args, vals)
			next++
		}
		query = injectAfter(query, leaderboardsRangeAnchor, pred)
	}

	// Requester-scoped rename: re-group by remapped project/language (only the
	// requester's rows relabel; every other sender's project/language pass through).
	if rs.HasAxis("project") || rs.HasAxis("language") {
		ensureRequester()
		reqCond := fmt.Sprintf("sender = $%d", requesterArg)
		projExpr, langExpr := "project", "language"
		projExpr, args, next = rs.remapExpr("project", "project", reqCond, next, args)
		langExpr, args, next = rs.remapExpr("language", "language", reqCond, next, args)
		query = fmt.Sprintf(`SELECT %s AS project, %s AS language, sender, CAST(SUM(total_seconds) AS int8) AS total_seconds
FROM ( %s ) base
GROUP BY %s, %s, sender`, projExpr, langExpr, trimSQL(query), projExpr, langExpr)
	}

	out := []LeaderboardRow{}
	err := d.aggQuery(ctx, query, args, func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var r LeaderboardRow
			if err := rows.Scan(&r.Project, &r.Language, &r.Sender, &r.TotalSeconds); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// GetTotalTimeToday runs get_time_today.sql ($1 user).
// timeTodayRangeAnchor is the inner day-bound clause of get_time_today.sql; the
// hide exclusion is spliced after it (raw heartbeats scan, all axes available).
const timeTodayRangeAnchor = "time_sent < (current_date + interval '1' day)"

// GetTotalTimeToday returns today's attributed coding time, excluding the sender's
// hidden axis values (so the statusbar total matches the hidden dashboards).
func (d *DB) GetTotalTimeToday(ctx context.Context, user string, hs HiddenSets) (int64, error) {
	query := qGetTimeToday
	args := []any{user}
	if hs.AnyHidden() {
		pred, argsWith, _ := exclusionPredicate(hs, rawHeartbeatCols, 2, args)
		query = injectAfter(query, timeTodayRangeAnchor, pred)
		args = argsWith
	}
	var total int64
	err := d.Pool.QueryRow(ctx, query, args...).Scan(&total)
	return total, err
}

// GetTotalActivityTime runs get_total_project_time.sql ($1 user,$2 days,$3 project).
func (d *DB) GetTotalActivityTime(ctx context.Context, user string, days int64, project string) (int64, error) {
	var total int64
	err := d.Pool.QueryRow(ctx, qGetTotalProject, user, days, project).Scan(&total)
	return total, err
}

// GetTotalTimeBetween runs get_time_between.sql for a set of (user,project,min,max)
// ranges. Returns results in ascending order (reverse of the DESC insert order),
// matching hakatime's Database.getTotalTimeBetween.
func (d *DB) GetTotalTimeBetween(ctx context.Context, users, projects []string, mins, maxs []time.Time) ([]int64, error) {
	rows, err := d.Pool.Query(ctx, qGetTimeBetween, users, projects, mins, maxs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ---- Projects, tags, badges ----

// CheckProjectOwner reports whether the user owns the given project.
func (d *DB) CheckProjectOwner(ctx context.Context, user, project string) (bool, error) {
	var name string
	err := d.Pool.QueryRow(ctx, `SELECT name FROM projects WHERE name = $1 AND owner = $2`, project, user).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// CheckTagOwner reports whether the user owns the given tag.
func (d *DB) CheckTagOwner(ctx context.Context, user, tag string) (bool, error) {
	var name string
	err := d.Pool.QueryRow(ctx, `
		SELECT name FROM tags
		INNER JOIN project_tags ON tags.id = project_tags.tag_id
		WHERE name = $1 AND project_owner = $2 LIMIT 1`, tag, user).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// GetTags returns tags on a project.
func (d *DB) GetTags(ctx context.Context, user, project string) ([]string, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT name FROM project_tags
		INNER JOIN tags ON project_tags.tag_id = tags.id
		WHERE project_name = $1 AND project_owner = $2`, project, user)
	if err != nil {
		return nil, err
	}
	return collectStrings(rows)
}

// GetAllTags returns all distinct tags for a user.
func (d *DB) GetAllTags(ctx context.Context, user string) ([]string, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT DISTINCT name FROM project_tags
		INNER JOIN tags ON project_tags.tag_id = tags.id
		WHERE project_owner = $1`, user)
	if err != nil {
		return nil, err
	}
	return collectStrings(rows)
}

// projectListCols maps hide axes to their heartbeats-qualified columns for the
// projects-list join. A project only surfaces if it has heartbeats not matching
// any hidden value, so a project consisting solely of hidden activity disappears.
var projectListCols = map[string]string{
	"project": "heartbeats.project", "language": "heartbeats.language",
	"editor": "heartbeats.editor", "plugin": "heartbeats.plugin",
	"machine": "heartbeats.machine", "platform": "heartbeats.platform",
	"branch": "heartbeats.branch", "category": "heartbeats.category",
}

// GetAllProjects returns a user's projects with (non-hidden) heartbeats in
// [t0,t1], most active first. All hide axes are excluded on the heartbeats join.
// A project rename relabels names at read time (raw rows untouched): merged names
// collapse to one entry, most active first.
func (d *DB) GetAllProjects(ctx context.Context, user string, t0, t1 time.Time, hs HiddenSets, rs RenameSets) ([]string, error) {
	query := `
		SELECT name, count(*) AS cnt FROM projects
		INNER JOIN heartbeats ON heartbeats.project = projects.name AND heartbeats.sender = projects.owner
		WHERE heartbeats.sender = $1 AND heartbeats.time_sent >= $2 AND heartbeats.time_sent <= $3`
	args := []any{user, t0, t1}
	next := 4
	if hs.AnyHidden() {
		var pred string
		pred, args, next = exclusionPredicate(hs, projectListCols, next, args)
		query += pred
	}
	query += ` GROUP BY projects.name`

	// Always re-project to exactly `name` (collectStrings reads one column). When a
	// project rename is active, remap+re-group so merged names collapse into one
	// entry ordered by summed activity; otherwise pass names through by count.
	nameExpr := "name"
	if rs.HasAxis("project") {
		nameExpr, args, next = rs.remapExpr("project", "name", "", next, args)
	}
	query = fmt.Sprintf(`SELECT %s AS name FROM ( %s ) base GROUP BY %s ORDER BY SUM(cnt) DESC`,
		nameExpr, trimSQL(query), nameExpr)

	rows, err := d.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return collectStrings(rows)
}

// SetTags replaces the tags on a project and returns the number added.
func (d *DB) SetTags(ctx context.Context, user, project string, tags []string) (int64, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	tagIDs := make([]uuid.UUID, 0, len(tags))
	for _, t := range tags {
		var id uuid.UUID
		if err := tx.QueryRow(ctx,
			`INSERT INTO tags (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name RETURNING id`,
			t).Scan(&id); err != nil {
			return 0, err
		}
		tagIDs = append(tagIDs, id)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_tags WHERE project_name = $1 AND project_owner = $2`, project, user); err != nil {
		return 0, err
	}
	var added int64
	for _, id := range tagIDs {
		ct, err := tx.Exec(ctx,
			`INSERT INTO project_tags (project_name, project_owner, tag_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			project, user, id)
		if err != nil {
			return 0, err
		}
		added += ct.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return added, nil
}

// CreateBadgeLink upserts a badge link and returns its id.
func (d *DB) CreateBadgeLink(ctx context.Context, user, project string) (uuid.UUID, error) {
	var id uuid.UUID
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO badges(username, project) VALUES ($1, $2)
		ON CONFLICT (username, project) DO UPDATE SET username=EXCLUDED.username
		RETURNING link_id`, user, project).Scan(&id)
	return id, err
}

// GetBadgeLinkInfo returns the (username, project) for a badge id.
func (d *DB) GetBadgeLinkInfo(ctx context.Context, id uuid.UUID) (string, string, error) {
	var user, project string
	err := d.Pool.QueryRow(ctx, `SELECT username, project FROM badges WHERE link_id = $1`, id).Scan(&user, &project)
	return user, project, err
}

func collectStrings(rows pgx.Rows) ([]string, error) {
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

// Import-job storage lives in importjobs.go (durable, resumable job records).
