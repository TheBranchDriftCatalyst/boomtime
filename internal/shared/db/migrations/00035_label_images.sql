-- +goose Up
-- +goose StatementBegin

-- label_images (boom-myv): one distinctive image per memeification label
-- archetype/tribe, shared across ALL users. Label ids are the same catalog
-- for everyone — so the same emblem shows up on every user's profile who
-- has earned "Late Night Coder" (etc.). Primary key is `label_id` alone
-- (NOT (owner, label_id)) — the point of the feature is a single image per
-- archetype, not a per-user emblem.
--
-- Storage is bytea + mime_type — the image is small (<50 KB PNG typically),
-- served by GET /api/v1/labels/:id/image with a 1-year immutable
-- Cache-Control. `model` + `prompt` + `seed` are provenance columns:
-- when the operator swaps `BOOM_COMFYUI_MODEL` and regenerates via
-- `boomtime label-images regenerate --all`, the row records which pipeline
-- and prompt produced the current bytes.
--
-- `generated_at` doubles as the client-side cache-bust hint: the frontend
-- appends ?v=<generated_at.epoch> to the <img src> so a regeneration bust
-- the browser-side immutable cache without any Cache-Control gymnastics.

CREATE TABLE public.label_images (
    label_id TEXT PRIMARY KEY,
    image_bytes BYTEA NOT NULL,
    mime_type TEXT NOT NULL DEFAULT 'image/png',
    model TEXT,
    prompt TEXT,
    seed BIGINT,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.label_images;
-- +goose StatementEnd
