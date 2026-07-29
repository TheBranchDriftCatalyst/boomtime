-- gaka-dg7: day boundary computed in user-local TZ ($6) so the branch daily
-- series lines up with the day series the project-daily-extras + top-level
-- daily total use. All three must bucket the same way or the FE overlays go
-- crooked.
--
-- Per-day branch activity for one project (coalesce null branch -> 'Other').
-- Gap-conditional SUM over precomputed gap_seconds, mirroring get_projects_stats.
-- Returns one row per (day, branch) plus the pct/daily_pct windows so the Go
-- shaper can build ResourceStats aligned to the same day series as DailyTotal.
-- $1 sender, $2 project, $3 start, $4 end, $5 limit (minutes), $6 IANA tz name.
WITH stats AS (
    SELECT
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)::date + interval '0h' AS day,
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
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)::date + interval '0h',
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
