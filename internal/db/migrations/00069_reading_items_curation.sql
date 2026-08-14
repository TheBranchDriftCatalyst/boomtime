-- +goose Up
-- +goose StatementBegin

-- catalyst-books curation override layer (gaka-books). reading_items.status /
-- finished_at / rating are the DERIVED layer: Amazon-owned, recomputed every
-- sync. These four columns are the OVERRIDE layer: written ONLY by the user (the
-- PATCH /books/items/:id/curation endpoint) or by the Hardcover pull's
-- last-writer-wins branch — NEVER by ingest. effective = COALESCE(override,
-- derived), computed centrally in the query DSL.
--
--   status_override        — user/Hardcover chosen status (want|reading|read|paused|dnf)
--   rating_override        — user chosen rating
--   finished_at_override   — user chosen finish date
--   curation_updated_at    — the single row-level LWW stamp for the override layer
--                            (compared against hardcover_remote_updated_at on pull)
--
-- hardcover_pushed_at already exists (migration 00063, declared-never-written);
-- the push path starts writing it now as the echo-suppression stamp, paired with
-- hardcover_pushed_status (the status string we last pushed) so the pull can tell
-- our own echo apart from a genuine remote Hardcover edit.
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS status_override      text,
    ADD COLUMN IF NOT EXISTS rating_override      numeric,
    ADD COLUMN IF NOT EXISTS finished_at_override timestamptz,
    ADD COLUMN IF NOT EXISTS curation_updated_at  timestamptz,
    ADD COLUMN IF NOT EXISTS hardcover_pushed_status text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS status_override,
    DROP COLUMN IF EXISTS rating_override,
    DROP COLUMN IF EXISTS finished_at_override,
    DROP COLUMN IF EXISTS curation_updated_at,
    DROP COLUMN IF EXISTS hardcover_pushed_status;
-- +goose StatementEnd
