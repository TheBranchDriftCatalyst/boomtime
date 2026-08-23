-- +goose Up
-- +goose StatementBegin

-- Kindle Reading-Insights snapshot (boom-books): ONE row per user holding the
-- raw /kindle/reading/insights/data response verbatim. SILOED like
-- reading_items / reading_activity — ON DELETE CASCADE with the user, never
-- writes into heartbeats/stats. The finish-DATE half of the payload
-- (goal_info.titles_read) is projected onto reading_items.finished_at by the
-- ingest; this table retains the WHOLE payload (streaks, goals, achievements)
-- as JSONB so a future insights surface can render them WITHOUT a schema churn
-- now — new attributes need no migration.
--   owner       — the user (PK: one current snapshot per user, upserted)
--   raw         — the full insights response body
--   fetched_at  — when this snapshot was captured
CREATE TABLE IF NOT EXISTS public.kindle_reading_insights (
    owner      text        PRIMARY KEY REFERENCES public.users(username) ON DELETE CASCADE,
    raw        jsonb       NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.kindle_reading_insights;
-- +goose StatementEnd
