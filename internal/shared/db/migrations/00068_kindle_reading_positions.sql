-- +goose Up
-- +goose StatementBegin

-- Kindle last-page-read POSITION samples (boom-books): the raw time-series the
-- FORWARD reading-TIME composition gap-sums into reading SESSIONS. Each poll of
-- the Fiona CDE sidecar appends one row per in-progress kindle book holding the
-- current last-page-read position + when it was observed. SILOED like
-- reading_items / reading_activity — ON DELETE CASCADE with the user, never
-- writes into heartbeats/stats.
--
-- This is the Kindle analogue of coding heartbeats: consecutive samples whose
-- position ADVANCED within a session-gap threshold are "reading"; the composition
-- (internal/domains/books/reading_time.go) diffs them into reading_activity
-- (source='kindle') day buckets so Kindle reading-time unifies with Audible
-- listening-time under the reading `seconds` measure.
--
--   owner       — the user
--   asin        — the book (reading_items.external_id for source='kindle')
--   position    — the last-page-read location (an integer offset; opaque locator)
--   sampled_at  — when this position was observed (the poll time)
-- The UNIQUE(owner, asin, sampled_at) makes an accidental double-poll at the same
-- instant a no-op (ON CONFLICT DO NOTHING), keeping sample capture idempotent.
CREATE TABLE IF NOT EXISTS public.kindle_reading_positions (
    id         bigserial   PRIMARY KEY,
    owner      text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    asin       text        NOT NULL,
    position   bigint      NOT NULL,
    sampled_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, asin, sampled_at)
);

-- The composition reads samples for one book ordered by sampled_at; this index
-- serves both the (owner, asin, since) range scan and the ordering.
CREATE INDEX IF NOT EXISTS kindle_reading_positions_owner_asin_sampled_idx
    ON public.kindle_reading_positions (owner, asin, sampled_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.kindle_reading_positions;
-- +goose StatementEnd
