-- Per-day coding time grouped by heartbeat category (coalesce null -> 'Other').
-- Gap-conditional SUM over precomputed gap_seconds, mirroring get_user_activity.
-- Excludes hidden projects via an appended `AND NOT (project = ANY($n))` after the
-- range-end anchor (see categoryDailyRangeAnchor). Returns pct/daily_pct windows
-- so the Go shaper can build ResourceStats aligned to the same day series.
-- $1 sender, $2 start, $3 end, $4 limit (minutes).
WITH stats AS (
    SELECT
        time_sent::date + interval '0h' AS day,
        coalesce(category, 'Other') AS category,
        CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
    FROM
        heartbeats
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3
    GROUP BY
        time_sent::date + interval '0h',
        coalesce(category, 'Other')
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
