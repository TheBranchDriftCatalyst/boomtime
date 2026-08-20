-- 00081_reading_events_enriched.sql — a read-only VIEW that enriches each discrete
-- read in reading_events (the multi-read HISTORY, one row per read) with the book
-- metadata carried on reading_items (title/authors/series/genres/status). It is the
-- table the query DSL's `readingEvents` domain (internal/shared/query/domains.go)
-- reads: grouped `reads` counts by origin/source/series/author/genre/status, plus a
-- per-event leaf-rows projection.
--
-- The book match is a LEFT JOIN LATERAL so an event ALWAYS survives even when its
-- book row is missing (a read with no matching reading_items row still counts, its
-- metadata columns just NULL). Match precedence: the Amazon edition (source +
-- external_id) when the event carries one, else the cross-edition Hardcover Work id.
-- LIMIT 1 keeps it one book per event.
-- +goose Up
-- +goose StatementBegin
CREATE VIEW public.reading_events_enriched AS
SELECT
    re.owner,
    re.origin,
    re.source,
    re.external_id,
    re.hardcover_book_id,
    re.started_at,
    re.finished_at,
    re.progress_pages,
    re.progress_seconds,
    ri.title,
    ri.authors,
    ri.series,
    ri.genres,
    -- status (raw item status) + status_override are both exposed so the reused
    -- `status` dimension's COALESCE(status_override, status) resolves on the view;
    -- status_effective is the precomputed effective value for the row projection.
    ri.status,
    ri.status_override,
    COALESCE(ri.status_override, ri.status) AS status_effective
FROM public.reading_events re
LEFT JOIN LATERAL (
    SELECT *
    FROM public.reading_items i
    WHERE i.owner = re.owner
      AND (
          (re.source <> '' AND i.source = re.source AND i.external_id = re.external_id)
          OR (re.hardcover_book_id IS NOT NULL AND i.hardcover_book_id = re.hardcover_book_id)
      )
    LIMIT 1
) ri ON true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS public.reading_events_enriched;
-- +goose StatementEnd
