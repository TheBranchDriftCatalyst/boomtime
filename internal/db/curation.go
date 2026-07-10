package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Curation actions.
const (
	CurationHide   = "hide"
	CurationRename = "rename"
)

// CurationRule is a per-user data-curation rule (hide or rename) on a label axis.
type CurationRule struct {
	ID         int       `json:"id"`
	Axis       string    `json:"axis"`
	Action     string    `json:"action"`
	MatchValue string    `json:"matchValue"`
	NewValue   *string   `json:"newValue"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListCurationRules returns a user's rules, newest first.
func (d *DB) ListCurationRules(ctx context.Context, sender string) ([]CurationRule, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, axis, action, match_value, new_value, created_at
		FROM curation_rules WHERE sender = $1 ORDER BY id DESC`, sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CurationRule{}
	for rows.Next() {
		var r CurationRule
		if err := rows.Scan(&r.ID, &r.Axis, &r.Action, &r.MatchValue, &r.NewValue, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateCurationRule inserts a rule (deduped on sender,axis,action,match_value)
// and returns it. On an existing duplicate it returns the current row.
func (d *DB) CreateCurationRule(ctx context.Context, sender, axis, action, matchValue string, newValue *string) (*CurationRule, error) {
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO curation_rules (sender, axis, action, match_value, new_value)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (sender, axis, action, match_value)
		DO UPDATE SET new_value = EXCLUDED.new_value
		RETURNING id, axis, action, match_value, new_value, created_at`,
		sender, axis, action, matchValue, newValue)
	var r CurationRule
	if err := row.Scan(&r.ID, &r.Axis, &r.Action, &r.MatchValue, &r.NewValue, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetCurationRule fetches a single rule by id (no owner filter; caller checks).
func (d *DB) GetCurationRule(ctx context.Context, id int) (*CurationRule, string, error) {
	var r CurationRule
	var sender string
	err := d.Pool.QueryRow(ctx, `
		SELECT id, axis, action, match_value, new_value, created_at, sender
		FROM curation_rules WHERE id = $1`, id).
		Scan(&r.ID, &r.Axis, &r.Action, &r.MatchValue, &r.NewValue, &r.CreatedAt, &sender)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &r, sender, nil
}

// DeleteCurationRule removes a rule (owner-scoped). Returns rows affected.
func (d *DB) DeleteCurationRule(ctx context.Context, sender string, id int) (int64, error) {
	ct, err := d.Pool.Exec(ctx, `DELETE FROM curation_rules WHERE id = $1 AND sender = $2`, id, sender)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// ---- Rename application (backfill existing rows; merges into new_value) ----

// ApplyRename renames all of a sender's heartbeats where <col>=matchValue to
// new_value, then rebuilds derived data (gaps + rollup). For axis=project it also
// migrates projects/project_tags/badges so the heartbeats FK stays satisfied and
// the old project row is removed (merge). Returns the number of heartbeats moved.
func (d *DB) ApplyRename(ctx context.Context, sender, axis, matchValue, newValue string) (int64, error) {
	col, ok := ExploreColumn(axis)
	if !ok {
		return 0, fmt.Errorf("axis %q is not renamable", axis)
	}
	if axis == "day" {
		return 0, errors.New("the day axis cannot be renamed")
	}

	var moved int64
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if axis == "project" {
		// Ensure the target project row exists so the heartbeats FK (ON UPDATE
		// CASCADE) is satisfied when we point rows at new_value.
		if _, err := tx.Exec(ctx,
			`INSERT INTO projects (owner, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			sender, newValue); err != nil {
			return 0, err
		}
		// Move heartbeats to the new project name (merge if it already existed).
		ct, err := tx.Exec(ctx,
			`UPDATE heartbeats SET project = $3 WHERE sender = $1 AND project = $2`,
			sender, matchValue, newValue)
		if err != nil {
			return 0, err
		}
		moved = ct.RowsAffected()

		// Re-point project_tags to the new name, de-duplicating on conflict.
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_tags (project_name, project_owner, tag_id)
			SELECT $3, project_owner, tag_id FROM project_tags
			WHERE project_owner = $1 AND project_name = $2
			ON CONFLICT DO NOTHING`, sender, matchValue, newValue); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM project_tags WHERE project_owner = $1 AND project_name = $2`,
			sender, matchValue); err != nil {
			return 0, err
		}
		// Move/dedupe badges (badges have a unique (username,project) constraint).
		if _, err := tx.Exec(ctx, `
			UPDATE badges SET project = $3
			WHERE username = $1 AND project = $2
			  AND NOT EXISTS (SELECT 1 FROM badges WHERE username = $1 AND project = $3)`,
			sender, matchValue, newValue); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM badges WHERE username = $1 AND project = $2`,
			sender, matchValue); err != nil {
			return 0, err
		}
		// Remove the now-empty old project row (FK-safe: no heartbeats/tags/badges
		// reference it anymore).
		if _, err := tx.Exec(ctx,
			`DELETE FROM projects WHERE owner = $1 AND name = $2`, sender, matchValue); err != nil {
			return 0, err
		}
	} else {
		// Non-project axes have no FK — a plain UPDATE merges naturally.
		q := fmt.Sprintf(`UPDATE heartbeats SET %s = $3 WHERE sender = $1 AND %s = $2`, col, col)
		ct, err := tx.Exec(ctx, q, sender, matchValue, newValue)
		if err != nil {
			return 0, err
		}
		moved = ct.RowsAffected()
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	// Renames don't change time order, so gap_seconds is unaffected — but the
	// rollup groups by project/language/editor/platform/machine, so rebuild it
	// fully from the epoch for this sender.
	if err := d.RefreshRollup(ctx, sender, time.Unix(0, 0).UTC()); err != nil {
		return moved, err
	}
	return moved, nil
}

// ---- Rename canonicalization on ingest ----

// renameRule is a compact (axis -> match -> new) lookup for canonicalizing beats.
type renameMap map[string]map[string]string

// loadRenameMap builds the sender's active rename rules keyed by axis then match.
func (d *DB) loadRenameMap(ctx context.Context, sender string) (renameMap, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT axis, match_value, new_value FROM curation_rules WHERE sender = $1 AND action = 'rename' AND new_value IS NOT NULL`,
		sender)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := renameMap{}
	for rows.Next() {
		var axis, match, newv string
		if err := rows.Scan(&axis, &match, &newv); err != nil {
			return nil, err
		}
		if m[axis] == nil {
			m[axis] = map[string]string{}
		}
		m[axis][match] = newv
	}
	return m, rows.Err()
}

func strPtr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// canonicalize rewrites a value for one axis per the rename map (or returns it
// unchanged). Operates on the pointer targets used by HeartbeatPayload.
func (m renameMap) apply(axis string, v *string) *string {
	if v == nil {
		return nil
	}
	if byMatch, ok := m[axis]; ok {
		if nv, ok := byMatch[*v]; ok {
			return &nv
		}
	}
	return v
}

// ---- Hide exclusion helpers ----

// HiddenValues returns the set of hidden match_values for one axis (action=hide).
func (d *DB) HiddenValues(ctx context.Context, sender, axis string) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT match_value FROM curation_rules WHERE sender = $1 AND axis = $2 AND action = 'hide'`,
		sender, axis)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// hiddenAxes is the definitive set of curation-hide axes excluded from the
// aggregate dashboards. Suppressing a value on any of these axes removes it from
// stats/projects/big-bet dashboards. Ordered for deterministic SQL/arg building.
var hiddenAxes = []string{
	"project", "language", "editor", "plugin", "machine", "platform", "branch", "category",
}

// HiddenSets holds the hidden values per axis (axis name -> match_values). Empty
// or absent slices mean "exclude nothing" for that axis.
type HiddenSets struct {
	byAxis map[string][]string
}

// Values returns the hidden values for one axis (nil if none).
func (h HiddenSets) Values(axis string) []string { return h.byAxis[axis] }

// Projects is a convenience accessor for the project axis (project-only paths).
func (h HiddenSets) Projects() []string { return h.byAxis["project"] }

// AnyHidden reports whether any axis has a hidden value.
func (h HiddenSets) AnyHidden() bool {
	for _, v := range h.byAxis {
		if len(v) > 0 {
			return true
		}
	}
	return false
}

// HasHiddenOutside reports whether any hidden axis is NOT in the provided
// available set. Used to decide whether a pre-aggregated path (e.g. the rollup,
// which lacks some columns) must fall back to the raw heartbeats scan.
func (h HiddenSets) HasHiddenOutside(available map[string]bool) bool {
	for axis, vals := range h.byAxis {
		if len(vals) > 0 && !available[axis] {
			return true
		}
	}
	return false
}

// exclusionPredicate builds `AND NOT (<col> = ANY($n))` clauses for each hidden
// axis present in cols (axis -> SQL column expression). Axes absent from cols are
// skipped (e.g. columns a pre-aggregated table lacks). Values are passed as array
// params, so this is injection-safe. Returns the SQL fragment, grown args, and
// next free arg index.
func exclusionPredicate(hs HiddenSets, cols map[string]string, nextArg int, args []any) (string, []any, int) {
	var sql string
	for _, axis := range hiddenAxes { // deterministic order
		vals := hs.byAxis[axis]
		col := cols[axis]
		if len(vals) == 0 || col == "" {
			continue
		}
		sql += fmt.Sprintf(" AND NOT (%s = ANY($%d))", col, nextArg)
		args = append(args, vals)
		nextArg++
	}
	return sql, args, nextArg
}

// rawHeartbeatCols maps every hidden axis to its column on the raw heartbeats
// table. Used by all queries whose innermost scan is `heartbeats` (all axes are
// available). `type` is stored in the ty column but is not a hide axis here.
var rawHeartbeatCols = map[string]string{
	"project": "project", "language": "language", "editor": "editor",
	"plugin": "plugin", "machine": "machine", "platform": "platform",
	"branch": "branch", "category": "category",
}

// LoadHiddenSets fetches the hidden values for every dashboard-excluded axis.
func (d *DB) LoadHiddenSets(ctx context.Context, sender string) (HiddenSets, error) {
	hs := HiddenSets{byAxis: make(map[string][]string, len(hiddenAxes))}
	for _, axis := range hiddenAxes {
		vals, err := d.HiddenValues(ctx, sender, axis)
		if err != nil {
			return hs, err
		}
		if len(vals) > 0 {
			hs.byAxis[axis] = vals
		}
	}
	return hs, nil
}
