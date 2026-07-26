// label_images.go: DB accessors for the shared per-archetype label image
// blobs (gaka-myv). One row per label id — the same image is served to every
// user who earns that label.
//
// The endpoint that fronts this table is public (no auth), so accessors here
// stay narrow: get by id, save (upsert) by id, and a cheap existence check
// the worker uses to skip labels that already have a rendered image.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// LabelImage is the row shape read by the public image endpoint. `Seed` and
// `Model`/`Prompt` are provenance — surfaced only in ops workflows (CLI
// regenerate), never returned to end users.
type LabelImage struct {
	LabelID     string
	ImageBytes  []byte
	MimeType    string
	Model       string
	Prompt      string
	Seed        *int64
	GeneratedAt time.Time
}

// GetLabelImage returns the stored image for `id`, or (nil, false, nil) when
// no row is present. Missing => 404 in the handler. Any other error is
// bubbled unchanged.
func (d *DB) GetLabelImage(ctx context.Context, id string) (*LabelImage, bool, error) {
	if id == "" {
		return nil, false, errors.New("GetLabelImage: empty id")
	}
	row := d.Pool.QueryRow(ctx, `
		SELECT label_id, image_bytes, mime_type,
		       COALESCE(model, ''), COALESCE(prompt, ''), seed, generated_at
		  FROM label_images
		 WHERE label_id = $1`, id)
	var li LabelImage
	if err := row.Scan(&li.LabelID, &li.ImageBytes, &li.MimeType,
		&li.Model, &li.Prompt, &li.Seed, &li.GeneratedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &li, true, nil
}

// SaveLabelImage upserts the row for `id`. When `mimeType` is empty it
// defaults to image/png (also the column default). `seed` may be nil when
// the shim response omits a resolved seed.
//
// Upsert semantics let the CLI `boomtime label-images regenerate` overwrite
// an existing row without a separate delete step, and refresh
// generated_at so the FE's ?v=<epoch> cache-bust flips.
func (d *DB) SaveLabelImage(ctx context.Context, id string, bytes []byte, mimeType, model, prompt string, seed *int64) error {
	if id == "" {
		return errors.New("SaveLabelImage: empty id")
	}
	if len(bytes) == 0 {
		return errors.New("SaveLabelImage: empty bytes")
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO label_images (label_id, image_bytes, mime_type, model, prompt, seed, generated_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, now())
		ON CONFLICT (label_id) DO UPDATE
		   SET image_bytes  = EXCLUDED.image_bytes,
		       mime_type    = EXCLUDED.mime_type,
		       model        = EXCLUDED.model,
		       prompt       = EXCLUDED.prompt,
		       seed         = EXCLUDED.seed,
		       generated_at = now()`,
		id, bytes, mimeType, model, prompt, seed)
	return err
}

// HasLabelImage is the cheap existence probe the worker uses to skip labels
// that already have a rendered image, so a restart is idempotent.
func (d *DB) HasLabelImage(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, errors.New("HasLabelImage: empty id")
	}
	var one int
	err := d.Pool.QueryRow(ctx, `SELECT 1 FROM label_images WHERE label_id = $1`, id).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteLabelImage removes a single row so the next generation attempt
// starts from scratch. Used by `boomtime label-images regenerate --id`.
// Idempotent — no error when the row is absent.
func (d *DB) DeleteLabelImage(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("DeleteLabelImage: empty id")
	}
	_, err := d.Pool.Exec(ctx, `DELETE FROM label_images WHERE label_id = $1`, id)
	return err
}

// TruncateLabelImages wipes every row so `regenerate --all` starts clean.
// Used by the CLI regenerate --all path.
func (d *DB) TruncateLabelImages(ctx context.Context) error {
	_, err := d.Pool.Exec(ctx, `DELETE FROM label_images`)
	return err
}

// LabelImageMeta is the row minus the bytes — for admin listings that
// need to show status/size/generated_at per label without shipping every
// PNG blob down the wire.
type LabelImageMeta struct {
	ID          string    `json:"id"`
	SizeBytes   int64     `json:"sizeBytes"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// ListLabelImagesMeta returns metadata for every row in label_images —
// no image bytes. Powers the Admin tab's per-label status table (gaka-myv).
// Ordered by generated_at DESC so the newest lands first (irrelevant to
// the FE which keys by id, but stable-order helps snapshot tests).
func (d *DB) ListLabelImagesMeta(ctx context.Context) ([]LabelImageMeta, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT label_id, octet_length(image_bytes)::bigint, generated_at
		 FROM label_images
		 ORDER BY generated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LabelImageMeta, 0, 64)
	for rows.Next() {
		var m LabelImageMeta
		if err := rows.Scan(&m.ID, &m.SizeBytes, &m.GeneratedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteLabelImages batches DeleteLabelImage over a slice. One round trip
// instead of len(ids) — the per-id loop tripped the N+1 detector on the
// admin regenerate path (gaka-myv) where ~70 ids come in per click.
// Empty slice is a no-op. Nil-safe.
func (d *DB) DeleteLabelImages(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := d.Pool.Exec(ctx,
		`DELETE FROM label_images WHERE label_id = ANY($1::text[])`, ids)
	return err
}
