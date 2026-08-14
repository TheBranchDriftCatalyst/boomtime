-- +goose Up
-- +goose StatementBegin

-- catalyst-books deep-link fix (gaka-qic0): Hardcover book PAGES resolve on the
-- book's SLUG (hardcover.app/books/<slug>), NOT the numeric book id — a
-- matched-but-slugless row 404s. Persist the slug alongside the resolved match so
-- the "open on Hardcover" link lands on the real page.
--
--   reading_items.hardcover_slug    — the per-row cached slug (mirrors
--                                     hardcover_book_id; NULL until a re-match /
--                                     re-pull backfills it).
--   hardcover_match_cache.book_slug — the slug carried in the GLOBAL (gaka-wzgr)
--                                     cross-user match cache so a CACHE HIT also
--                                     yields the slug (else cache-hit matches
--                                     would still 404).
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS hardcover_slug text;

ALTER TABLE public.hardcover_match_cache
    ADD COLUMN IF NOT EXISTS book_slug text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS hardcover_slug;
ALTER TABLE public.hardcover_match_cache
    DROP COLUMN IF EXISTS book_slug;
-- +goose StatementEnd
