// widgets.go holds the widget-link CRUD for the public embeddable SVG widgets
// (gaka-hsj). Mirrors the badge-link pattern in projects.go: an auth'd upsert
// mints a stable uuid per (user, scope); the public render endpoint resolves
// the uuid back to its scope. Widget kind/range/theme are URL params, not rows.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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

// WidgetLink is one minted widget link (Settings list view). ScopeName is
// resolved via join for the space scope (the space's `name`); for project it
// equals ScopeRef; for user it's empty. Origins is a bounded set of {origin,
// count, lastSeen} tuples updated on each public SVG hit — RecordWidgetLinkHit
// caps it at 20 most-recent entries.
type WidgetLink struct {
	LinkID     uuid.UUID     `json:"linkId"`
	ScopeType  string        `json:"scopeType"`
	ScopeRef   string        `json:"scopeRef"`
	ScopeName  string        `json:"scopeName,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
	LastUsedAt *time.Time    `json:"lastUsedAt,omitempty"`
	Origins    []OriginStat  `json:"origins,omitempty"`
}

// OriginStat is one referring URL (or "direct") that has fetched the public
// widget SVG, with a running count and the last-seen timestamp.
type OriginStat struct {
	Origin   string    `json:"origin"`
	Count    int       `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
}

const originsCap = 20

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
// LEFT JOINs spaces so a space-scoped link carries its space's name — the
// row label is "Marketing" not "Space #6".
func (d *DB) ListWidgetLinks(ctx context.Context, user string) ([]WidgetLink, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT wl.link_id, wl.scope_type, wl.scope_ref, wl.created_at,
		       wl.last_used_at, wl.origins,
		       CASE
		           WHEN wl.scope_type = 'space' THEN s.name
		           WHEN wl.scope_type = 'project' THEN wl.scope_ref
		           ELSE ''
		       END AS scope_name
		FROM widget_links wl
		LEFT JOIN spaces s
		    ON wl.scope_type = 'space'
		   AND s.id::text = wl.scope_ref
		   AND s.owner = wl.username
		WHERE wl.username = $1
		ORDER BY wl.created_at DESC, wl.scope_type, wl.scope_ref`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WidgetLink{}
	for rows.Next() {
		var wl WidgetLink
		var raw []byte
		var name *string
		if err := rows.Scan(&wl.LinkID, &wl.ScopeType, &wl.ScopeRef, &wl.CreatedAt,
			&wl.LastUsedAt, &raw, &name); err != nil {
			return nil, err
		}
		if name != nil {
			wl.ScopeName = *name
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &wl.Origins)
		}
		out = append(out, wl)
	}
	return out, rows.Err()
}

// DeleteWidgetLink was removed. Delete-then-mint had confusing UX (once the
// scope's link was gone there was no obvious re-mint path from Settings) and
// RollWidgetLink covers the actual "invalidate a leaked/screenshotted URL"
// use case without leaving the scope in a link-less state.

// RollWidgetLink mints a new uuid for an existing link, in place. Same pattern
// as rolling an API token: the scope + created_at stay the same, but the
// SECRET (the uuid) changes. Any embed using the old id immediately 404s,
// while a fresh mint URL for the same scope points at the new one — so a
// leaked/screenshotted URL can be revoked without losing your own embed's
// meaning. Owner-scoped: a cross-owner id yields (nil, false, nil) so the
// handler returns 404 without touching another user's row.
func (d *DB) RollWidgetLink(ctx context.Context, user string, oldID uuid.UUID) (uuid.UUID, bool, error) {
	var newID uuid.UUID
	err := d.Pool.QueryRow(ctx, `
		UPDATE widget_links SET link_id = uuid_generate_v4()
		WHERE username = $1 AND link_id = $2
		RETURNING link_id`, user, oldID).Scan(&newID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return newID, true, nil
}

// RecordWidgetLinkHit updates the per-link usage stats on each public SVG
// fetch: bumps last_used_at, and merges `origin` into the bounded origins
// set (bump count if seen before, otherwise append; cap at originsCap). Uses
// a serializable transaction (FOR UPDATE) so concurrent hits don't lose
// counts to a lost-update race.
//
// origin is the Referer header if present, else "direct". GitHub camo strips
// Referer on repeated fetches, so most README embeds show up as "direct" —
// that's fine, it's still a useful signal ("someone is embedding this").
// A missing link (id doesn't exist) is a no-op — the public handler will
// 404 anyway; this call is fire-and-forget from the caller's perspective.
func (d *DB) RecordWidgetLinkHit(ctx context.Context, id uuid.UUID, origin string) error {
	if origin == "" {
		origin = "direct"
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var raw []byte
	err = tx.QueryRow(ctx,
		`SELECT origins FROM widget_links WHERE link_id = $1 FOR UPDATE`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // link doesn't exist; hit isn't ours to track
	}
	if err != nil {
		return err
	}
	var origins []OriginStat
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &origins)
	}
	now := time.Now().UTC()
	found := false
	for i := range origins {
		if origins[i].Origin == origin {
			origins[i].Count++
			origins[i].LastSeen = now
			found = true
			break
		}
	}
	if !found {
		origins = append(origins, OriginStat{Origin: origin, Count: 1, LastSeen: now})
	}
	// Most-recent first, cap at originsCap. A well-behaved embed (one origin
	// hit often) never triggers the cap; the cap protects against a script
	// that varies Referer per request.
	sort.SliceStable(origins, func(i, j int) bool {
		return origins[i].LastSeen.After(origins[j].LastSeen)
	})
	if len(origins) > originsCap {
		origins = origins[:originsCap]
	}
	buf, err := json.Marshal(origins)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE widget_links SET last_used_at = $2, origins = $3 WHERE link_id = $1`,
		id, now, buf); err != nil {
		return err
	}
	return tx.Commit(ctx)
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

// ProjectMemberSetWithRenames is ProjectMemberSet expanded via the owner's
// rename map so a scope pinned to a renamed/merged project name matches raw
// heartbeats stored under the source name(s) (gaka-xuc). The `project` arm
// becomes `[project] ∪ ExactSourcesFor("project", project)` — the display
// name plus every raw name that renames to it. Regex/template renames are
// intentionally left out (see ExactSourcesFor comment).
func ProjectMemberSetWithRenames(project string, rs RenameSets) MemberSets {
	exact := []string{project}
	for _, src := range rs.ExactSourcesFor("project", project) {
		if src != project {
			exact = append(exact, src)
		}
	}
	return MemberSets{byAxis: map[string]axisMembers{
		"project": {exact: exact},
	}}
}
