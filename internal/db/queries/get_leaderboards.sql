-- Phase A: windowless conditional SUM over precomputed gap_seconds, grouped by
-- project/language/sender. gap_seconds is per-sender by construction, which also
-- fixes the previous cross-user lag() bug. $1 start, $2 end. (15-min limit.)
SELECT
    coalesce(project, 'Other') AS project,
    coalesce(language, 'Other') AS "language",
    sender,
    CAST(sum(CASE WHEN gap_seconds <= (15 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
FROM
    heartbeats
WHERE
    time_sent >= $1
    AND time_sent <= $2
GROUP BY
    project,
    language,
    sender
ORDER BY
    language
