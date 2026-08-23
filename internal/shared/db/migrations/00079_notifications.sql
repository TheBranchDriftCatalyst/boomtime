-- 00079_notifications.sql — DURABLE notifications (boom-books). The notify hub is
-- an in-process WS fan-out: an event fired while the user has no open session is
-- dropped on the floor. Durable events are ALSO written here, so they survive a
-- missing session and are delivered when the user next connects (fetched on mount +
-- marked read). Ephemeral events (toast-only) never touch this table. Owner-scoped;
-- read_at NULL = unread. data is the Event's structured payload (e.g. the finished
-- book's asin/source), kept for the FE to deep-link.
-- +goose Up
CREATE TABLE IF NOT EXISTS public.notifications (
    id         bigserial   PRIMARY KEY,
    owner      text        NOT NULL,
    type       text        NOT NULL,
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    data       jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz
);

-- The list query is per-owner, newest first; unread filtering rides the same index.
CREATE INDEX IF NOT EXISTS idx_notifications_owner_created
    ON public.notifications (owner, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS public.notifications;
