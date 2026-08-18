-- +goose Up
-- +goose StatementBegin

-- catalyst-books: Hardcover is a bidirectional external DATASOURCE, not a thing we
-- mirror. We persist only the MINIMAL linkage needed to reconcile a reading_item
-- with the user's Hardcover shelf + suppress write-echoes; any book DETAILS are
-- fetched from Hardcover on demand (internal/hardcover). No local shelf copy.
--
--   hardcover_book_id / _edition_id — the resolved match (cache; never re-fuzz)
--   hardcover_status                — last status we know for it on the shelf (diff basis)
--   hardcover_match_confidence      — 'asin' | 'isbn' | 'search' | 'none'
--   hardcover_matched_at            — when the match was resolved (idempotent re-match)
--   hardcover_pushed_at             — last write WE made out (echo-suppression on pull)
--   hardcover_remote_updated_at     — Hardcover's own updated_at at last sight
--                                     (future bi-di: delta detection + last-writer-wins)
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS hardcover_book_id           bigint,
    ADD COLUMN IF NOT EXISTS hardcover_edition_id        bigint,
    ADD COLUMN IF NOT EXISTS hardcover_status            text,
    ADD COLUMN IF NOT EXISTS hardcover_match_confidence  text,
    ADD COLUMN IF NOT EXISTS hardcover_matched_at        timestamptz,
    ADD COLUMN IF NOT EXISTS hardcover_pushed_at         timestamptz,
    ADD COLUMN IF NOT EXISTS hardcover_remote_updated_at timestamptz;

-- Partial index: the match/reconcile passes scan only already-linked rows.
CREATE INDEX IF NOT EXISTS reading_items_hardcover_book_idx
    ON public.reading_items (owner, hardcover_book_id)
    WHERE hardcover_book_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.reading_items_hardcover_book_idx;
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS hardcover_book_id,
    DROP COLUMN IF EXISTS hardcover_edition_id,
    DROP COLUMN IF EXISTS hardcover_status,
    DROP COLUMN IF EXISTS hardcover_match_confidence,
    DROP COLUMN IF EXISTS hardcover_matched_at,
    DROP COLUMN IF EXISTS hardcover_pushed_at,
    DROP COLUMN IF EXISTS hardcover_remote_updated_at;
-- +goose StatementEnd
