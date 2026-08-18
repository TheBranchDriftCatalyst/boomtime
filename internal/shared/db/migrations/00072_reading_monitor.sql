-- +goose Up
-- +goose StatementBegin

-- Persistent server-side Kindle reading-monitor (gaka-books §5.1). Two additions:
--
-- (1) Per-user toggle + mode on the users row. reading_monitor_enabled drives the
--     leader-singleton two-level engine (books-reading-monitor kind) whether or
--     not the admin panel is open — turn it on, walk away, come back to the
--     report. reading_monitor_mode is the toast verbosity:
--       'debounced' — one coalesced toast per advancing book / status change
--                     (normal use)
--       'verbose'   — a toast on every observed advance (the reverse-engineering
--                     / diagnostic phase)
ALTER TABLE public.users
    ADD COLUMN IF NOT EXISTS reading_monitor_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS reading_monitor_mode    text    NOT NULL DEFAULT 'debounced';

-- (2) Per-book monitor state — the state the two-level engine carries ACROSS
--     ticks so it can (a) detect a last-page-read ADVANCE vs the stored position,
--     and (b) drive the L1 (coarse detect) → L2 (fine capture) → idle transitions.
--     SILOED like kindle_reading_positions / reading_items: ON DELETE CASCADE with
--     the user, never writes into heartbeats/stats.
--
--   owner              — the user
--   asin               — the book (reading_items.external_id for source='kindle')
--   last_location      — the furthest last-page-read position we've observed
--   last_advance_at    — Amazon's creationTime for the last observed advance (the
--                        EVENT time, not the poll time); drives the idle-gap G test
--                        and the advance-interval histogram
--   last_polled_at     — when the engine last polled this book (the L1/L2 cadence
--                        clock: a book is "due" when now-last_polled_at >= its
--                        level interval)
--   active             — true while the book is in L2 (fine capture); flips back to
--                        false after no advance for the idle gap G
--   updated_at         — bookkeeping
CREATE TABLE IF NOT EXISTS public.kindle_reading_monitor_state (
    owner           text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    asin            text        NOT NULL,
    last_location   bigint      NOT NULL DEFAULT 0,
    last_advance_at timestamptz,
    last_polled_at  timestamptz,
    active          boolean     NOT NULL DEFAULT false,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner, asin)
);

-- The engine loads a user's whole state set each pass, and the gauge counts
-- active books; index on (owner) with a partial on active serves both.
CREATE INDEX IF NOT EXISTS kindle_reading_monitor_state_active_idx
    ON public.kindle_reading_monitor_state (owner) WHERE active;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.kindle_reading_monitor_state;
ALTER TABLE public.users
    DROP COLUMN IF EXISTS reading_monitor_enabled,
    DROP COLUMN IF EXISTS reading_monitor_mode;
-- +goose StatementEnd
