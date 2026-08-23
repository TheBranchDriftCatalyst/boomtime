-- boom-dg7: day boundary computed in user-local TZ ($5) so a 23:59 PT commit
-- (07:59 UTC next day) is credited to the right day. Streak / rollup / daily
-- percentage calculations all downstream of this — before the fix a Pacific
-- user's evening work bled forward to "tomorrow".
--
-- Phase A: "time spent" = SUM of precomputed inter-heartbeat gaps (gap_seconds)
-- that are within the timeLimit ($4 minutes). No per-query lag() window / sort;
-- gap_seconds is materialized at ingest.
-- $1 sender, $2 start, $3 end, $4 limit (minutes), $5 IANA tz name.
WITH stats AS (
    SELECT
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)::date + interval '0h' AS day,
        coalesce(project, 'Other') AS project,
        coalesce(language, 'Other') AS LANGUAGE,
        coalesce(editor, 'Other') AS editor,
        coalesce(branch, 'Other') AS branch,
        coalesce(platform, 'Other') AS platform,
        coalesce(machine, 'Other') AS machine,
        entity,
        CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds,
        -- boom-6ci: NULL-axis discriminator. Well-defined here because the
        -- GROUP BY uses raw columns (not COALESCE'd), so every row in a
        -- group shares the same NULL-ness on each axis.
        (project IS NULL) AS project_missing,
        (language IS NULL) AS language_missing,
        (editor IS NULL) AS editor_missing,
        (branch IS NULL) AS branch_missing,
        (platform IS NULL) AS platform_missing,
        (machine IS NULL) AS machine_missing
    FROM
        heartbeats
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3
    GROUP BY
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)::date + interval '0h',
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
    day, project, language, editor, branch, platform, machine, entity, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct,
    project_missing, language_missing, editor_missing,
    branch_missing, platform_missing, machine_missing
FROM
    stats
