// widget_defs.go holds the persistence layer for gaka-3nu: named/saved custom
// widget compositions. Sibling to widget_links but a distinct concept —
// widget_defs stores WHAT the widget renders (the composition spec), while
// widget_links stores WHERE it's scoped and how it's shared. v1 keeps defs
// user-scoped (no space/project scope) — the point is naming + stability.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WidgetDef is one saved composition. Spec is the widget.Def JSON as raw
// bytes — the handler decodes/validates it before rendering.
type WidgetDef struct {
	DefID     uuid.UUID       `json:"defId"`
	Name      string          `json:"name"`
	Spec      json.RawMessage `json:"spec"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// CreateWidgetDef inserts a new named def for (user, name). Fails on
// (username, name) conflict — the caller should use UpdateWidgetDef to
// iterate on a saved composition.
func (d *DB) CreateWidgetDef(ctx context.Context, user, name string, spec json.RawMessage) (uuid.UUID, error) {
	var id uuid.UUID
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO widget_defs(username, name, spec) VALUES ($1, $2, $3)
		RETURNING def_id`, user, name, spec).Scan(&id)
	return id, err
}

// GetWidgetDef fetches a def by id. Owner is returned so the public renderer
// can apply the owner's curation. ok=false with nil error means no such def.
func (d *DB) GetWidgetDef(ctx context.Context, id uuid.UUID) (owner string, def WidgetDef, ok bool, err error) {
	err = d.Pool.QueryRow(ctx, `
		SELECT def_id, username, name, spec, created_at, updated_at
		FROM widget_defs
		WHERE def_id = $1`, id).
		Scan(&def.DefID, &owner, &def.Name, &def.Spec, &def.CreatedAt, &def.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", WidgetDef{}, false, nil
	}
	if err != nil {
		return "", WidgetDef{}, false, err
	}
	return owner, def, true, nil
}

// GetWidgetDefByName is the "iterate on my saved widget" lookup — used by the
// builder to load a def for editing without knowing its uuid.
func (d *DB) GetWidgetDefByName(ctx context.Context, user, name string) (WidgetDef, bool, error) {
	var def WidgetDef
	err := d.Pool.QueryRow(ctx, `
		SELECT def_id, name, spec, created_at, updated_at
		FROM widget_defs
		WHERE username = $1 AND name = $2`, user, name).
		Scan(&def.DefID, &def.Name, &def.Spec, &def.CreatedAt, &def.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WidgetDef{}, false, nil
	}
	if err != nil {
		return WidgetDef{}, false, err
	}
	return def, true, nil
}

// ListWidgetDefs returns every saved def a user owns (Settings + builder
// picker). Ordered most-recently-updated first.
func (d *DB) ListWidgetDefs(ctx context.Context, user string) ([]WidgetDef, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT def_id, name, spec, created_at, updated_at
		FROM widget_defs
		WHERE username = $1
		ORDER BY updated_at DESC, name`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WidgetDef{}
	for rows.Next() {
		var wd WidgetDef
		if err := rows.Scan(&wd.DefID, &wd.Name, &wd.Spec, &wd.CreatedAt, &wd.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, wd)
	}
	return out, rows.Err()
}

// UpdateWidgetDef replaces the spec for an existing def (owner + name key).
// Returns ok=false when no matching row exists. updated_at bumps automatically.
func (d *DB) UpdateWidgetDef(ctx context.Context, user, name string, spec json.RawMessage) (bool, error) {
	tag, err := d.Pool.Exec(ctx, `
		UPDATE widget_defs SET spec = $3, updated_at = now()
		WHERE username = $1 AND name = $2`, user, name, spec)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// DeleteWidgetDef removes a saved def (owner-scoped). Follow-on embeds using
// the def's uuid immediately 404 — the point is exactly to break them.
func (d *DB) DeleteWidgetDef(ctx context.Context, user, name string) (bool, error) {
	tag, err := d.Pool.Exec(ctx, `
		DELETE FROM widget_defs WHERE username = $1 AND name = $2`, user, name)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
