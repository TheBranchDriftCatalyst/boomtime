-- 00078_reading_events.sql — discrete READ events (a book can be read more than
-- once). reading_items holds the CURRENT/latest state (one row per book with one
-- finished_at); reading_events holds the HISTORY: each read's start/finish/progress.
-- Hardcover is the authoritative multi-read source (its user_book_reads carry a
-- stable id); Amazon finishes contribute one event each. The UNIQUE(owner, origin,
-- external_read_id) makes re-ingest an idempotent UPSERT — re-running the pipeline
-- never duplicates a read. Distinct from reading_activity (the time-series heartbeat
-- layer): events are discrete reads, activity is per-day reading seconds.
-- +goose Up
CREATE TABLE IF NOT EXISTS public.reading_events (
    id                bigserial   PRIMARY KEY,
    owner             text        NOT NULL,
    -- The book this read belongs to. hardcover_book_id when known (the cross-edition
    -- Work id); external_id (ASIN) + source identify the Amazon edition.
    source            text        NOT NULL DEFAULT '',
    external_id       text        NOT NULL DEFAULT '',
    hardcover_book_id bigint,
    -- origin: hardcover | audible | kindle-insights — who produced this read.
    origin            text        NOT NULL,
    -- external_read_id: the origin's stable id for this read (Hardcover
    -- user_book_reads.id; for Amazon, asin+finish-date). The idempotency key.
    external_read_id  text        NOT NULL,
    started_at        timestamptz,
    finished_at       timestamptz,
    progress_pages    int,
    progress_seconds  int,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, origin, external_read_id)
);

-- Panel/history reads: by owner + book.
CREATE INDEX IF NOT EXISTS reading_events_owner_book_idx
    ON public.reading_events (owner, hardcover_book_id);
CREATE INDEX IF NOT EXISTS reading_events_owner_ext_idx
    ON public.reading_events (owner, source, external_id);

-- +goose Down
DROP TABLE IF EXISTS public.reading_events;
