// goals.go — CRUD accessors for user-defined composite goals (gaka-wpb).
//
// One row per (owner, name). `spec` is an opaque JSONB blob validated
// upstream by internal/stats/goals.go before we ever see it; from this
// layer's perspective the tree is just bytes. `last_progress` is a
// separate JSONB blob written by the evaluator (via UpdateGoalProgress);
// mutating spec ALWAYS clears it so the next read recomputes under the
// new definition.
//
// Owner scoping is enforced on every path — a caller passing another
// user's id gets (nil, ...) back exactly like a missing row (never leak
// existence).
package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Goal is the persisted goal row. Spec + LastProgress are opaque JSON
// blobs to this package — the evaluator/handler own their shape. Kept as
// json.RawMessage (not any) so the exact bytes survive DB roundtrips.
type Goal struct {
	ID              string          `json:"id"`
	Owner           string          `json:"owner"`
	Name            string          `json:"name"`
	Description     *string         `json:"description"`
	Spec            json.RawMessage `json:"spec"`
	Enabled         bool            `json:"enabled"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	LastEvaluatedAt *time.Time      `json:"lastEvaluatedAt"`
	LastProgress    json.RawMessage `json:"lastProgress"`
}

// scanGoal centralizes the column ordering used by every SELECT so a schema
// tweak only touches this one line.
const goalCols = `id, owner, name, description, spec, enabled, created_at, updated_at, last_evaluated_at, last_progress`

func scanGoal(row pgx.Row) (*Goal, error) {
	var g Goal
	err := row.Scan(
		&g.ID, &g.Owner, &g.Name, &g.Description, &g.Spec, &g.Enabled,
		&g.CreatedAt, &g.UpdatedAt, &g.LastEvaluatedAt, &g.LastProgress,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// ListGoals returns every goal for owner, newest first.
func (d *DB) ListGoals(ctx context.Context, owner string) ([]Goal, error) {
	if owner == "" {
		return nil, errors.New("ListGoals: empty owner")
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT `+goalCols+` FROM goals WHERE owner = $1 ORDER BY created_at DESC, id`,
		owner,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Goal{}
	for rows.Next() {
		var g Goal
		if err := rows.Scan(
			&g.ID, &g.Owner, &g.Name, &g.Description, &g.Spec, &g.Enabled,
			&g.CreatedAt, &g.UpdatedAt, &g.LastEvaluatedAt, &g.LastProgress,
		); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGoal fetches one goal, owner-scoped. Returns (nil, nil) when the id is
// absent OR belongs to another user (indistinguishable — never leak
// existence). Any other error is bubbled unchanged.
func (d *DB) GetGoal(ctx context.Context, owner, id string) (*Goal, error) {
	if owner == "" || id == "" {
		return nil, errors.New("GetGoal: empty owner/id")
	}
	row := d.Pool.QueryRow(ctx,
		`SELECT `+goalCols+` FROM goals WHERE id = $1 AND owner = $2`,
		id, owner,
	)
	return scanGoal(row)
}

// CreateGoal inserts a new goal. Spec is stored verbatim as JSONB; the
// caller must have validated it. Returns the created row (with server-side
// id, timestamps, and default enabled=true). A duplicate (owner, name)
// pair returns a wrapped pgx unique-violation error the handler surfaces
// as 409.
func (d *DB) CreateGoal(ctx context.Context, owner, name string, description *string, spec json.RawMessage) (*Goal, error) {
	if owner == "" || name == "" {
		return nil, errors.New("CreateGoal: empty owner/name")
	}
	if len(spec) == 0 {
		return nil, errors.New("CreateGoal: empty spec")
	}
	row := d.Pool.QueryRow(ctx,
		`INSERT INTO goals (owner, name, description, spec)
		 VALUES ($1, $2, $3, $4::jsonb)
		 RETURNING `+goalCols,
		owner, name, description, string(spec),
	)
	return scanGoal(row)
}

// GoalPatch collects the mutable fields for PATCH. Any non-nil field is
// written; nil leaves the column untouched. Spec is a *json.RawMessage so a
// nil pointer means "don't touch"; a non-nil-but-empty message is rejected
// upstream. When Spec is written, LastProgress + LastEvaluatedAt are cleared
// atomically so the next read recomputes under the new definition.
type GoalPatch struct {
	Name        *string
	Description *string
	Spec        *json.RawMessage
	Enabled     *bool
}

// UpdateGoal writes the patch fields owner-scoped. Returns (nil, nil) when
// the id is absent or belongs to another user (never leak existence). A
// duplicate (owner, name) on rename returns a wrapped pgx unique-violation
// error the handler surfaces as 409.
//
// Building the UPDATE dynamically (COALESCE trick would work but hurts
// EXPLAIN readability) keeps unaffected columns untouched at the wire level
// too — updated_at only ticks when SOMETHING actually changed via the patch.
func (d *DB) UpdateGoal(ctx context.Context, owner, id string, patch GoalPatch) (*Goal, error) {
	if owner == "" || id == "" {
		return nil, errors.New("UpdateGoal: empty owner/id")
	}
	// Build SET fragment + args list. $1 always = id, $2 = owner (WHERE).
	// Subsequent $N bind the patch fields in the order they're appended.
	args := []any{id, owner}
	sets := []string{}
	nextArg := 3
	if patch.Name != nil {
		sets = append(sets, "name = $"+itoaFast(nextArg))
		args = append(args, *patch.Name)
		nextArg++
	}
	if patch.Description != nil {
		sets = append(sets, "description = $"+itoaFast(nextArg))
		args = append(args, *patch.Description)
		nextArg++
	}
	if patch.Spec != nil {
		if len(*patch.Spec) == 0 {
			return nil, errors.New("UpdateGoal: empty spec")
		}
		sets = append(sets, "spec = $"+itoaFast(nextArg)+"::jsonb")
		args = append(args, string(*patch.Spec))
		nextArg++
		// Spec change invalidates the cached progress — set to NULL so the
		// next GET /progress recomputes under the new definition.
		sets = append(sets, "last_progress = NULL")
		sets = append(sets, "last_evaluated_at = NULL")
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = $"+itoaFast(nextArg))
		args = append(args, *patch.Enabled)
		nextArg++
	}
	if len(sets) == 0 {
		// No-op patch — return the current row so callers can treat this like
		// a plain GET (idempotent PATCH is a nicer API than 400 on empty).
		return d.GetGoal(ctx, owner, id)
	}
	sets = append(sets, "updated_at = now()")

	q := `UPDATE goals SET ` + joinComma(sets) + ` WHERE id = $1 AND owner = $2 RETURNING ` + goalCols
	row := d.Pool.QueryRow(ctx, q, args...)
	return scanGoal(row)
}

// DeleteGoal removes a goal, owner-scoped. Returns (true, nil) when a row
// was removed, (false, nil) when the id is absent or belongs to another
// user (never leak existence).
func (d *DB) DeleteGoal(ctx context.Context, owner, id string) (bool, error) {
	if owner == "" || id == "" {
		return false, errors.New("DeleteGoal: empty owner/id")
	}
	ct, err := d.Pool.Exec(ctx, `DELETE FROM goals WHERE id = $1 AND owner = $2`, id, owner)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ToggleGoal flips (or sets, when `desired` is non-nil) the enabled flag,
// owner-scoped. Returns (newEnabled, found). One SQL statement — no
// read-modify-write TOCTOU on concurrent double-clicks. When `desired` is
// non-nil, an idempotent no-op (already at desired value) still returns
// found=true.
func (d *DB) ToggleGoal(ctx context.Context, owner, id string, desired *bool) (newEnabled bool, found bool, err error) {
	if owner == "" || id == "" {
		return false, false, errors.New("ToggleGoal: empty owner/id")
	}
	if desired != nil {
		// Idempotent set. Two-step so we can distinguish "not found" from
		// "found but already at desired value" (UPDATE with matching value
		// affects 0 rows in Postgres).
		ct, err := d.Pool.Exec(ctx,
			`UPDATE goals SET enabled = $3, updated_at = now() WHERE id = $1 AND owner = $2`,
			id, owner, *desired)
		if err != nil {
			return false, false, err
		}
		if ct.RowsAffected() > 0 {
			return *desired, true, nil
		}
		// Check whether the row exists at all before returning not-found.
		var one int
		err = d.Pool.QueryRow(ctx,
			`SELECT 1 FROM goals WHERE id = $1 AND owner = $2`, id, owner).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false, nil
		}
		if err != nil {
			return false, false, err
		}
		return *desired, true, nil
	}
	err = d.Pool.QueryRow(ctx,
		`UPDATE goals SET enabled = NOT enabled, updated_at = now()
		 WHERE id = $1 AND owner = $2
		 RETURNING enabled`, id, owner).Scan(&newEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return newEnabled, true, nil
}

// UpdateGoalProgress writes the evaluator's most recent output into the
// cache columns. Owner-scoped. `progress` is stored verbatim; passing nil
// clears the cache (used by heartbeat ingest invalidation).
func (d *DB) UpdateGoalProgress(ctx context.Context, owner, id string, progress json.RawMessage) error {
	if owner == "" || id == "" {
		return errors.New("UpdateGoalProgress: empty owner/id")
	}
	if progress == nil {
		_, err := d.Pool.Exec(ctx,
			`UPDATE goals SET last_progress = NULL, last_evaluated_at = NULL
			 WHERE id = $1 AND owner = $2`, id, owner)
		return err
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE goals SET last_progress = $3::jsonb, last_evaluated_at = now()
		 WHERE id = $1 AND owner = $2`,
		id, owner, string(progress))
	return err
}

// InvalidateGoalsForOwner clears the cached progress for every goal an
// owner has. Called from the heartbeat ingest path — new activity might
// have flipped a goal's status, so we can't serve stale progress.
// Idempotent — a call for an owner with no goals is a no-op.
func (d *DB) InvalidateGoalsForOwner(ctx context.Context, owner string) error {
	if owner == "" {
		return errors.New("InvalidateGoalsForOwner: empty owner")
	}
	_, err := d.Pool.Exec(ctx,
		`UPDATE goals SET last_progress = NULL, last_evaluated_at = NULL
		 WHERE owner = $1 AND last_progress IS NOT NULL`, owner)
	return err
}

// itoaFast is a tiny alloc-free int-to-string for the small $N values in
// the dynamic UPDATE builder (never wider than a few digits). strconv.Itoa
// would work; this avoids the strconv import elsewhere in the package if
// it isn't already pulled.
func itoaFast(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [8]byte{}
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// joinComma joins strings with ", " — tiny helper to avoid pulling strings
// into this file for a two-line concat.
func joinComma(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	n += 2 * (len(ss) - 1)
	buf := make([]byte, 0, n)
	for i, s := range ss {
		if i > 0 {
			buf = append(buf, ',', ' ')
		}
		buf = append(buf, s...)
	}
	return string(buf)
}
