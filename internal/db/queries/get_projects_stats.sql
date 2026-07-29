-- gaka-dg7: day/dow/hour buckets computed in user-local TZ ($6) so the
-- per-project stats page's dayofweek/hourofday drilldowns reflect the user's
-- clock, not UTC. Same fix as get_punchcard for badge misfires.
--
-- Phase A: windowless conditional SUM over precomputed gap_seconds for one
-- project. $1 sender, $2 project, $3 start, $4 end, $5 limit, $6 IANA tz name.
WITH stats AS (
    SELECT
        ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)::date + interval '0h' AS day,
        (CAST(extract(dow FROM (((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)::date + interval '0h')) AS int8))::text AS dayofweek,
        (CAST(extract(hour FROM ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)) AS int8))::text AS hourofday,
        coalesce(language, 'Other') AS LANGUAGE,
        entity,
        ty,
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
        extract(dow FROM (((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)::date + interval '0h')),
        extract(hour FROM ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $6)),
        language,
        entity,
        ty
    ORDER BY
        day
)
SELECT
    *,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats;
