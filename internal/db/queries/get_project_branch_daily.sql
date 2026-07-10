-- Per-day branch activity for one project (coalesce null branch -> 'Other').
-- Gap-conditional SUM over precomputed gap_seconds, mirroring get_projects_stats.
-- Returns one row per (day, branch) plus the pct/daily_pct windows so the Go
-- shaper can build ResourceStats aligned to the same day series as DailyTotal.
-- $1 sender, $2 project, $3 start, $4 end, $5 limit (minutes).
WITH stats AS (
    SELECT
        time_sent::date + interval '0h' AS day,
        coalesce(branch, 'Other') AS branch,
        CAST(sum(CASE WHEN gap_seconds <= ($5 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
    FROM
        heartbeats
    WHERE
        sender = $1
        AND project = $2
        AND time_sent >= $3
        AND time_sent <= $4
    GROUP BY
        time_sent::date + interval '0h',
        coalesce(branch, 'Other')
    ORDER BY
        day
)
SELECT
    day,
    branch,
    total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats;
