-- 00003_reading_events_enriched.sql — the standalone catalyst-books copy of the
-- host's reading_events_enriched VIEW (host migration 00081). The standalone mounts
-- the same cross-domain query DSL (POST /api/v1/query) for its library view, so the
-- `readingEvents` domain needs this view here too. Both reading_events (baseline
-- 00001, from host 00078) and reading_items (baseline 00001) exist in the standalone
-- FS, so the view definition is byte-identical to the host's.
--
-- Each discrete read in reading_events is LEFT JOIN LATERAL'd to its book row in
-- reading_items (Amazon edition source+external_id, else the Hardcover Work id),
-- exposing the event columns + the book metadata for grouping/leaf rows.
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
