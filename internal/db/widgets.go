// widgets.go holds the widget-link CRUD for the public embeddable SVG widgets
// (gaka-hsj). Mirrors the badge-link pattern in projects.go: an auth'd upsert
// mints a stable uuid per (user, scope); the public render endpoint resolves
// the uuid back to its scope. Widget kind/range/theme are URL params, not rows.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Widget link scope types (widget_links.scope_type CHECK).
const (
	WidgetScopeUser    = "user"
	WidgetScopeProject = "project"
	WidgetScopeSpace   = "space"
)

// WidgetLink is one minted widget link (Settings list view).
type WidgetLink struct {
	LinkID    uuid.UUID `json:"linkId"`
	ScopeType string    `json:"scopeType"`
	ScopeRef  string    `json:"scopeRef"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateWidgetLink upserts a widget link for (user, scope) and returns its id.
// Re-minting the same scope returns the existing uuid (stable embeds).
func (d *DB) CreateWidgetLink(ctx context.Context, user, scopeType, scopeRef string) (uuid.UUID, error) {
	var id uuid.UUID
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO widget_links(username, scope_type, scope_ref) VALUES ($1, $2, $3)
		ON CONFLICT (username, scope_type, scope_ref) DO UPDATE SET username=EXCLUDED.username
		RETURNING link_id`, user, scopeType, scopeRef).Scan(&id)
	return id, err
}

// GetWidgetLinkInfo resolves a widget link id to its (user, scopeType, scopeRef).
// ok is false (with a nil error) when no link exists — the missing-row
// convention used across this package instead of leaking pgx.ErrNoRows.
func (d *DB) GetWidgetLinkInfo(ctx context.Context, id uuid.UUID) (user, scopeType, scopeRef string, ok bool, err error) {
	err = d.Pool.QueryRow(ctx,
		`SELECT username, scope_type, scope_ref FROM widget_links WHERE link_id = $1`, id).
		Scan(&user, &scopeType, &scopeRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return user, scopeType, scopeRef, true, nil
}

// ListWidgetLinks returns every widget link a user has minted (Settings UI).
func (d *DB) ListWidgetLinks(ctx context.Context, user string) ([]WidgetLink, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT link_id, scope_type, scope_ref, created_at
		FROM widget_links WHERE username = $1
		ORDER BY created_at DESC, scope_type, scope_ref`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WidgetLink{}
	for rows.Next() {
		var wl WidgetLink
		if err := rows.Scan(&wl.LinkID, &wl.ScopeType, &wl.ScopeRef, &wl.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, wl)
	}
	return out, rows.Err()
}

// DeleteWidgetLink deletes a link owned by user. Returns false when no such
// link exists for THIS user — a cross-owner id must 404, never delete.
func (d *DB) DeleteWidgetLink(ctx context.Context, user string, id uuid.UUID) (bool, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM widget_links WHERE username = $1 AND link_id = $2`, user, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ProjectExists reports whether (owner, name) is a known project — the mint
// endpoint's ownership check for project-scoped widget links.
func (d *DB) ProjectExists(ctx context.Context, owner, name string) (bool, error) {
	var ok bool
	err := d.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE owner = $1 AND name = $2)`, owner, name).Scan(&ok)
	return ok, err
}

// ProjectMemberSet builds a MemberSets scoping to exactly one project — it lets
// project-scoped widgets reuse the whole Space inclusion-predicate path
// (spaceRequested=true semantics). Project is a rollup axis, so this keeps the
// rollup fast path too.
func ProjectMemberSet(project string) MemberSets {
	return MemberSets{byAxis: map[string]axisMembers{
		"project": {exact: []string{project}},
	}}
}
