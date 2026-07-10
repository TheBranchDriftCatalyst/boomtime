-- Phase A: "time spent" = SUM of precomputed inter-heartbeat gaps (gap_seconds)
-- that are within the timeLimit ($4 minutes). No per-query lag() window / sort;
-- gap_seconds is materialized at ingest. $1 sender, $2 start, $3 end, $4 limit.
WITH stats AS (
    SELECT
        time_sent::date + interval '0h' AS day,
        coalesce(project, 'Other') AS project,
        coalesce(language, 'Other') AS LANGUAGE,
        coalesce(editor, 'Other') AS editor,
        coalesce(branch, 'Other') AS branch,
        coalesce(platform, 'Other') AS platform,
        coalesce(machine, 'Other') AS machine,
        entity,
        CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
    FROM
        heartbeats
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3
    GROUP BY
        time_sent::date + interval '0h',
        project,
        language,
        editor,
        branch,
        platform,
        machine,
        entity
    ORDER BY
        day
)
SELECT
    *,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats
