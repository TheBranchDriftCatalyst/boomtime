-- +goose Up
-- +goose StatementBegin

-- The explicit MATCH stage of the catalyst-books pipeline (backfill → match →
-- sync) needs its own cursor: when did we last sweep this owner's still-unmatched
-- reading_items through the Hardcover match ladder. Rather than smear it across
-- reading_items it lives beside the other forward-sync cursors (migration 00062).
-- The match sweep is cross-source (it resolves both audible + kindle unmatched
-- rows), so it records last_match_at against a dedicated source='hardcover' row.
ALTER TABLE public.book_sync_state
    ADD COLUMN IF NOT EXISTS last_match_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.book_sync_state
    DROP COLUMN IF EXISTS last_match_at;
-- +goose StatementEnd
