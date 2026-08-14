-- +goose Up
-- +goose StatementBegin

-- catalyst-books negative/attempt cache (gaka-books): the hardcover-match sweep's
-- fuzzy tail is the expensive part — every still-unmatched row spends one live,
-- rate-limited Typesense search PER SWEEP, even when the last sweep already proved
-- Hardcover has no book for it. Stamp match_attempted_at when a row exhausts the
-- ladder with no confident hit so a repeat sweep can SKIP it, and only retry it
-- once the retry window elapses (in case Hardcover adds the book later).
--
--   reading_items.match_attempted_at — NULL until the ladder returns no-match for
--                                      this row; then now(). The sweep's candidate
--                                      load excludes rows attempted within the
--                                      retry window. An exact-id (asin/isbn) hit
--                                      never stamps it — those never reach the tail.
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS match_attempted_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS match_attempted_at;
-- +goose StatementEnd
