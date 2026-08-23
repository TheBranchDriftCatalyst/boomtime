-- boom-dg7: day boundary computed in user-local TZ ($5) — mirrors
-- get_user_activity so the category daily buckets align with the top-level
-- daily total series a Pacific user sees in the Overview.
--
-- Per-day coding time grouped by heartbeat category (coalesce null -> 'Other').
-- Gap-conditional SUM over precomputed gap_seconds, mirroring get_user_activity.
-- Excludes hidden projects via an appended `AND NOT (project = ANY($n))` after the
-- range-end anchor (see categoryDailyRangeAnchor). Returns pct/daily_pct windows
-- so the Go shaper can build ResourceStats aligned to the same day series.
-- $1 sender, $2 start, $3 end, $4 limit (minutes), $5 IANA tz name.
-- boom-6ci: filter out null-category heartbeats BEFORE aggregation. A
-- browser plugin or AI console tab whose category field wasn't set
-- shouldn't be silently classified as a real category — the category
-- pie is titled "categories," so only real categories belong. The
-- total-time card downstream still aggregates over all heartbeats
-- (different query path, no filter).
WITH stats AS (
    SELECT
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)::date + interval '0h' AS day,
        category,
        CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
    FROM
        heartbeats
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3
        AND category IS NOT NULL
    GROUP BY
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)::date + interval '0h',
        category
    ORDER BY
        day
)
SELECT
    day,
    category,
    total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats;
