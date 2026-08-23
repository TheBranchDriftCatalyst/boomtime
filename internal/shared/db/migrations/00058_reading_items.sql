-- +goose Up
-- +goose StatementBegin

-- catalyst-books / catalyst-audiobooks synced reading state (boom-books).
-- SILOED by design: one domain-owned table, ON DELETE CASCADE with the user,
-- and a per-user/per-source delete path (see internal/db/reading_items.go) so a
-- user can wipe their book data on request. It does NOT touch heartbeats /
-- stats / any core model — the fusion layer reads it, it never writes into core.
--
--   source          — 'audible' | 'kindle'
--   external_id      — ASIN (unique per owner+source)
--   progress_percent — 0..100 (Audible percent_complete; Kindle derived)
--   raw_meta         — the source item JSON, so new attributes need no migration
CREATE TABLE IF NOT EXISTS public.reading_items (
    id               bigserial   PRIMARY KEY,
    owner            text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source           text        NOT NULL,
    external_id      text        NOT NULL,
    title            text        NOT NULL DEFAULT '',
    authors          text        NOT NULL DEFAULT '',
    cover_url        text        NOT NULL DEFAULT '',
    status           text        NOT NULL DEFAULT '',
    progress_percent integer     NOT NULL DEFAULT 0,
    finished         boolean     NOT NULL DEFAULT false,
    started_at       timestamptz,
    finished_at      timestamptz,
    rating           numeric,
    raw_meta         jsonb,
    synced_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, source, external_id)
);

CREATE INDEX IF NOT EXISTS reading_items_owner_idx ON public.reading_items (owner);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.reading_items;
-- +goose StatementEnd
