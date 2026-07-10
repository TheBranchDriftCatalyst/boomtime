-- +goose Up
-- Phase A: precompute the inter-heartbeat gap (seconds to the previous heartbeat
-- for the same sender, in global time order). "Time spent" then becomes a plain
-- conditional SUM (sum of gaps <= timeLimit) instead of a per-query lag() window
-- scan — index-friendly, incrementally maintainable, and rollup-ready.
ALTER TABLE heartbeats ADD COLUMN IF NOT EXISTS gap_seconds INT;
-- Backfill existing rows (NULL for each sender's first heartbeat).
WITH seq AS (
    SELECT id,
        EXTRACT(EPOCH FROM (time_sent - lag(time_sent) OVER (PARTITION BY sender ORDER BY time_sent)))::int AS gap
    FROM heartbeats
)
UPDATE heartbeats h SET gap_seconds = seq.gap FROM seq WHERE h.id = seq.id;
-- +goose Down
ALTER TABLE heartbeats DROP COLUMN IF EXISTS gap_seconds;
