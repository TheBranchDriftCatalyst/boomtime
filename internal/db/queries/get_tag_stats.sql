-- Phase A: windowless conditional SUM over precomputed gap_seconds for all
-- projects carrying a tag. $1 sender, $2 tag, $3 start, $4 end, $5 limit.
WITH stats AS (
    SELECT
        heartbeats.time_sent::date + interval '0h' AS day,
        (CAST(extract(dow FROM (heartbeats.time_sent::date + interval '0h')) AS int8))::text AS dayofweek,
        (CAST(extract(hour FROM heartbeats.time_sent) AS int8))::text AS hourofday,
        coalesce(heartbeats.language, 'Other') AS LANGUAGE,
        heartbeats.entity,
        CAST(sum(CASE WHEN heartbeats.gap_seconds <= ($5 * 60) THEN heartbeats.gap_seconds ELSE 0 END) AS int8) AS total_seconds
    FROM
        heartbeats
        JOIN project_tags ON project_tags.project_owner = sender AND project_tags.project_name = project
        JOIN tags ON tags.id = project_tags.tag_id
    WHERE
        heartbeats.sender = $1
        AND tags.name = $2
        AND heartbeats.time_sent >= $3
        AND heartbeats.time_sent <= $4
    GROUP BY
        heartbeats.time_sent::date + interval '0h',
        extract(dow FROM (heartbeats.time_sent::date + interval '0h')),
        extract(hour FROM heartbeats.time_sent),
        heartbeats.language,
        heartbeats.entity
    ORDER BY
        day
)
SELECT
    *,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats;
