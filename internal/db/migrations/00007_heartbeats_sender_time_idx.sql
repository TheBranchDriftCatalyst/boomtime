-- +goose Up
-- Composite index so per-user aggregation queries (sender = X AND time_sent BETWEEN ...)
-- can seek by sender and read rows already ordered by time_sent (avoids scanning all
-- users' rows on multi-user instances and helps the window-function ordering).
CREATE INDEX IF NOT EXISTS heartbeats_sender_time_idx ON heartbeats (sender, time_sent);
-- +goose Down
DROP INDEX IF EXISTS heartbeats_sender_time_idx;
