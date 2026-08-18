// dashboard_layouts.go: DB accessors for the composable dashboard layout
// persistence layer (gaka-keb).
//
// One row per (owner, scope). Scope is a caller-controlled string ("public_profile"
// today; "overview", "projects" as we grow); the accessors intentionally do NOT
// enum-validate scope so a future scope only needs handler wiring, not a
// migration + code change here.
//
// Layout is opaque JSONB — the handler validates the top-level shape (well-formed
// JSON + optional cap on document size) and hands the raw bytes to us. The
// scrubber isn't relevant on this layer: the layout is placement metadata, not
// activity payload.
package db

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetDashboardLayout returns the persisted layout for (owner, scope). Returns
// (nil, false, nil) when no row is present — the handler surfaces that as a
// 404 to the caller (FE falls back to a default layout). Any other error is
// bubbled unchanged.
func (d *DB) GetDashboardLayout(ctx context.Context, owner, scope string) (json.RawMessage, bool, error) {
	if owner == "" || scope == "" {
		return nil, false, errors.New("GetDashboardLayout: empty owner/scope")
	}
	row := d.Pool.QueryRow(ctx,
		`SELECT layout FROM dashboard_layouts WHERE owner = $1 AND scope = $2`,
		owner, scope,
	)
	var raw json.RawMessage
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// SetDashboardLayout upserts the layout for (owner, scope). The stored bytes
// are byte-preserving relative to the caller's input (JSONB is not order-
// preserving by default in Postgres, but we store as `jsonb` here — the
// anti-tautology test (gaka-25r) covers the round-trip semantics; if you
// swap to a normalized storage in the future, that test will catch it).
func (d *DB) SetDashboardLayout(ctx context.Context, owner, scope string, layout json.RawMessage) error {
	if owner == "" || scope == "" {
		return errors.New("SetDashboardLayout: empty owner/scope")
	}
	if len(layout) == 0 {
		return errors.New("SetDashboardLayout: empty layout")
	}
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO dashboard_layouts (owner, scope, layout, updated_at)
		 VALUES ($1, $2, $3::jsonb, now())
		 ON CONFLICT (owner, scope)
		 DO UPDATE SET layout = EXCLUDED.layout, updated_at = now()`,
		owner, scope, string(layout),
	)
	return err
}

// DeleteDashboardLayout drops the persisted row so subsequent GETs return
// (nil, false, nil) and the FE reverts to the default layout. Idempotent —
// no error when the row is absent. Used by the "Reset to defaults" button.
func (d *DB) DeleteDashboardLayout(ctx context.Context, owner, scope string) error {
	if owner == "" || scope == "" {
		return errors.New("DeleteDashboardLayout: empty owner/scope")
	}
	_, err := d.Pool.Exec(ctx,
		`DELETE FROM dashboard_layouts WHERE owner = $1 AND scope = $2`,
		owner, scope,
	)
	return err
}
