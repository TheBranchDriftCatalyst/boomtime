// Package db — labels.go: CRUD for the DB-backed labels catalog (gaka-364.3).
//
// Rows land here from the initial seed (migration 00036) and, thereafter,
// from the admin CRUD UI. The public GET endpoint reads the whole table
// once per FE mount (staleTime 60s in useLabelsCatalog) so we don't cache
// heavily — a full 114-row scan of TEXT/JSONB is cheap.
//
// Design notes:
//
//   - `condition` is opaque JSONB to this package. The evaluator on the FE
//     owns its schema; from here we only care that the bytes round-trip.
//     Persist as json.RawMessage so pgx's json/jsonb path doesn't re-marshal
//     and reorder keys.
//   - Upsert is single-statement (ON CONFLICT DO UPDATE) so the admin PATCH
//     handler can call one method regardless of new/existing. Bulk-import
//     from a manifest reuses the same path.
//   - IDs are TEXT (stable slugs). No autoincrement — every id is meaningful
//     (persisted as a foreign key into label_images and, in the future,
//     could reach into user-award history if we start persisting awards).
//   - Owner-scoping is N/A — labels are global catalog data, not per-user.
//     Auth gating for writes lives at the handler layer (requireAdmin).
package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Label is the persisted catalog row. Description + OptimizedPrompt are
// nullable at the SQL level but modelled as strings — empty string means
// "unset" (mirrors how the JSON API surfaces it to the FE, where an
// undefined field is equivalent to an empty string for prompt purposes).
type Label struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	Label           string          `json:"label"`
	Glyph           string          `json:"glyph"`
	Description     string          `json:"description"`
	OptimizedPrompt string          `json:"optimizedPrompt"`
	Rank            int             `json:"rank"`
	Tier            string          `json:"tier"`
	Condition       json.RawMessage `json:"condition"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// labelCols is the canonical SELECT list. COALESCE the nullable text columns
// to '' so the Go side never has to distinguish NULL from empty — the
// admin editor stores empty-string for "no override" today and we want a
// consistent read shape.
const labelCols = `id, kind, label,
	COALESCE(glyph, ''),
	COALESCE(description, ''),
	COALESCE(optimized_prompt, ''),
	rank,
	COALESCE(tier, ''),
	condition, created_at, updated_at`

func scanLabel(row pgx.Row) (*Label, error) {
	var l Label
	if err := row.Scan(
		&l.ID, &l.Kind, &l.Label,
		&l.Glyph, &l.Description, &l.OptimizedPrompt,
		&l.Rank, &l.Tier, &l.Condition, &l.CreatedAt, &l.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// ListLabels returns every label row, ordered so the FE evaluator's sort is
// mostly stable at read time (kind bucketing, then rank DESC, then id for
// deterministic tie-break). The evaluator re-sorts anyway, but ordering
// here means snapshot tests / manifest exports are stable across dumps.
func (d *DB) ListLabels(ctx context.Context) ([]Label, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT `+labelCols+` FROM labels ORDER BY kind, rank DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Label, 0, 128)
	for rows.Next() {
		var l Label
		if err := rows.Scan(
			&l.ID, &l.Kind, &l.Label,
			&l.Glyph, &l.Description, &l.OptimizedPrompt,
			&l.Rank, &l.Tier, &l.Condition, &l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLabel returns the row for `id`, or (nil, nil) on miss. Never errors on
// missing — mirrors GetLabelImage's shape so handlers use the same
// nil-check pattern.
func (d *DB) GetLabel(ctx context.Context, id string) (*Label, error) {
	if id == "" {
		return nil, errors.New("GetLabel: empty id")
	}
	row := d.Pool.QueryRow(ctx,
		`SELECT `+labelCols+` FROM labels WHERE id = $1`, id)
	return scanLabel(row)
}

// UpsertLabel inserts a new row or updates every editable field on an
// existing one. The admin PATCH and bulk-import paths both use this.
//
// updated_at is refreshed on every write so the FE can invalidate its
// staleTime-60s query on rows that just changed. created_at is preserved
// (only set on the first INSERT).
//
// Kind is intentionally editable — the admin UI defaults it read-only in
// the sheet, but a future migration might promote a tribe → meme without
// needing an ALTER. If kind changes, the CHECK constraint enforces
// validity at the DB layer.
func (d *DB) UpsertLabel(ctx context.Context, l Label) error {
	if l.ID == "" {
		return errors.New("UpsertLabel: empty id")
	}
	if l.Kind == "" {
		return errors.New("UpsertLabel: empty kind")
	}
	if l.Label == "" {
		return errors.New("UpsertLabel: empty label")
	}
	if len(l.Condition) == 0 {
		return errors.New("UpsertLabel: empty condition JSONB")
	}
	// Normalize empty strings → NULL for optional columns via NULLIF so a
	// row round-tripped through the admin editor doesn't accumulate
	// meaningless '' values (a plain '' in glyph reads back as '' via the
	// COALESCE, which is fine, but NULL is truer to intent).
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO labels (
			id, kind, label, glyph, description, optimized_prompt,
			rank, tier, condition, updated_at
		) VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''),
		          $7, NULLIF($8,''), $9::jsonb, now())
		ON CONFLICT (id) DO UPDATE SET
			kind             = EXCLUDED.kind,
			label            = EXCLUDED.label,
			glyph            = EXCLUDED.glyph,
			description      = EXCLUDED.description,
			optimized_prompt = EXCLUDED.optimized_prompt,
			rank             = EXCLUDED.rank,
			tier             = EXCLUDED.tier,
			condition        = EXCLUDED.condition,
			updated_at       = now()`,
		l.ID, l.Kind, l.Label, l.Glyph, l.Description, l.OptimizedPrompt,
		l.Rank, l.Tier, string(l.Condition))
	return err
}

// DeleteLabel removes a single row. Idempotent — no error when the row is
// absent. Callers responsible for cascading label_images cleanup (call
// DeleteLabelImage separately if the label had a rendered image).
func (d *DB) DeleteLabel(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("DeleteLabel: empty id")
	}
	_, err := d.Pool.Exec(ctx, `DELETE FROM labels WHERE id = $1`, id)
	return err
}

// GetGenConfig returns the singleton generation-config's systemPrompt. On
// a fresh DB with only the initial row inserted by the migration, this
// returns "" (which the worker treats as "no prefix") until the manifest
// UPDATE lands or the admin edits it.
func (d *DB) GetGenConfig(ctx context.Context) (string, error) {
	var sp string
	err := d.Pool.QueryRow(ctx,
		`SELECT system_prompt FROM label_gen_config WHERE singleton = true`).Scan(&sp)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Should be impossible (migration seeds one row) but fail
			// soft — the worker treats "" as "no system prompt".
			return "", nil
		}
		return "", err
	}
	return sp, nil
}

// SetGenConfig updates the singleton row's systemPrompt. The row is created
// by the migration; we never INSERT here (the CHECK+PK on singleton
// prevents a second row anyway).
func (d *DB) SetGenConfig(ctx context.Context, systemPrompt string) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE label_gen_config
		   SET system_prompt = $1, updated_at = now()
		 WHERE singleton = true`, systemPrompt)
	return err
}
