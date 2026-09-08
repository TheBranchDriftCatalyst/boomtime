-- 00087_book_annotations_enriched.sql — a read-only VIEW enriching each
-- annotation in book_annotations (one row per HIGHLIGHT/NOTE/BOOKMARK/CLIP) with
-- the book metadata on reading_items. It is the table the query DSL's
-- `annotations` domain (internal/shared/query/domains.go) reads: `captures`
-- counts grouped by kind/source/series/author/genre/status, plus a per-annotation
-- leaf-rows projection.
--
-- Modelled on reading_events_enriched (00081) deliberately — same LEFT JOIN
-- LATERAL so an annotation ALWAYS survives when its book row is missing, and the
-- same status/status_override/status_effective triple so the shared `status`
-- dimension's COALESCE resolves on the view.
--
-- The join is simpler than the events one: an annotation always carries a
-- concrete (source, external_id), because it was fetched per-ASIN. There is no
-- Hardcover-Work fallback branch to write.
--
-- Note the domain split this exists to serve. Captures are NOT rows in
-- reading_events: that table is one-row-per-COMPLETED-READ with a finished_at,
-- and adding captures to it would inflate its `reads` measure and lie about what
-- a read is. A query domain has exactly one leaf-rows RowSource — which is why
-- events was split out of reading rather than added as a measure on it — and
-- annotations are a third leaf shape.
-- +goose Up
-- +goose StatementBegin
CREATE VIEW public.book_annotations_enriched AS
SELECT
    ba.owner,
    ba.source,
    ba.external_id,
    ba.kind,
    ba.annotation_key,
    ba.position_unit,
    ba.position_start,
    ba.position_end,
    ba.body,
    ba.note,
    ba.transcript,
    ba.transcript_source,
    ba.captured_at,
    ba.source_updated_at,
    ri.title,
    ri.authors,
    ri.series,
    ri.genres,
    ri.status,
    ri.status_override,
    COALESCE(ri.status_override, ri.status) AS status_effective
FROM public.book_annotations ba
LEFT JOIN LATERAL (
    SELECT *
    FROM public.reading_items i
    WHERE i.owner = ba.owner
      AND i.source = ba.source
      AND i.external_id = ba.external_id
    LIMIT 1
) ri ON true
WHERE ba.deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS public.book_annotations_enriched;
-- +goose StatementEnd
