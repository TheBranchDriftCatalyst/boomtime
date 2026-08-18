-- +goose Up
-- +goose StatementBegin

-- Per-day (or per-month) reading/listening activity time-series (gaka-books):
-- the grain the fusion layer overlays on the coding calendar. SILOED like
-- reading_items — ON DELETE CASCADE with the user, never writes into
-- heartbeats/stats. reading_items is the current-state table (one row per book);
-- this is a different grain, so it gets its own table rather than bolting buckets
-- onto reading_items.
--   source            — 'audible' | 'kindle' | 'amazon-export'
--   granularity       — 'day' | 'month'  (Audible aggregates support both)
--   bucket_date       — the bucket's start date (a DATE; for 'month', the 1st)
--   listening_seconds — Audible total_listening_stats for the bucket
--   pages             — Kindle/derived pages read in the bucket (nullable; audio has none)
-- The UNIQUE includes granularity so a 'day' bucket and a 'month' bucket for the
-- same start date coexist (backfill writes months, forward writes days).
CREATE TABLE IF NOT EXISTS public.reading_activity (
    id                bigserial   PRIMARY KEY,
    owner             text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source            text        NOT NULL,
    granularity       text        NOT NULL DEFAULT 'day',
    bucket_date       date        NOT NULL,
    listening_seconds bigint      NOT NULL DEFAULT 0,
    pages             integer,
    synced_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, source, bucket_date, granularity)
);

CREATE INDEX IF NOT EXISTS reading_activity_owner_idx
    ON public.reading_activity (owner);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.reading_activity;
-- +goose StatementEnd
