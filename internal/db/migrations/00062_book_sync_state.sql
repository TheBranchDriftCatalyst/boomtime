-- +goose Up
-- +goose StatementBegin

-- Forward-sync cursors for the catalyst-audiobooks/books domains (gaka-books).
-- Rather than smear cursor columns across reading_items, the per-user/per-source
-- delta bookkeeping lives here. SILOED — ON DELETE CASCADE with the user.
--   last_library_cursor  — newest library purchase_date seen (forward &purchased_after=)
--   last_finished_cursor — newest finished-sweep event_timestamp seen (forward start_date=)
--   last_activity_cursor — last aggregates window filled (reading_activity forward)
--   last_backfill_at     — NULL until the one-shot all-time backfill completes
--   last_forward_at      — last periodic forward-sync run
CREATE TABLE IF NOT EXISTS public.book_sync_state (
    owner                text NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source               text NOT NULL,
    last_library_cursor  timestamptz,
    last_finished_cursor timestamptz,
    last_activity_cursor date,
    last_backfill_at     timestamptz,
    last_forward_at      timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner, source)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.book_sync_state;
-- +goose StatementEnd
