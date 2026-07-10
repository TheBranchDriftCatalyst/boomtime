-- Phase A: windowless conditional SUM over precomputed gap_seconds, filtered to
-- projects carrying a tag. $1 sender, $2 start, $3 end, $4 tag, $5 limit.
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
        CAST(sum(CASE WHEN gap_seconds <= ($5 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
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
    day,
    project,
    LANGUAGE,
    editor,
    branch,
    platform,
    machine,
    entity,
    total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats
    INNER JOIN project_tags ON project_tags.project_name = stats.project
        AND project_tags.project_owner = $1
    INNER JOIN tags ON project_tags.tag_id = tags.id
WHERE
    tags.name = $4
