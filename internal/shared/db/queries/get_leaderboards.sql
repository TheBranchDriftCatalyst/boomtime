-- Phase A: windowless conditional SUM over precomputed gap_seconds, grouped by
-- project/language/sender. gap_seconds is per-sender by construction, which also
-- fixes the previous cross-user lag() bug. $1 start, $2 end. (15-min limit.)
-- boom-6ci: project_missing / language_missing carry the raw-NULL discriminator
-- so ToLeaderboardsPayload can build per-language sub-leaderboards WITHOUT the
-- 'Other' bucket (browser tabs / AI console with no language). The GLOBAL
-- leaderboard still sums over every row so total-time-per-user stays honest.
SELECT
    coalesce(project, 'Other') AS project,
    coalesce(language, 'Other') AS "language",
    sender,
    CAST(sum(CASE WHEN gap_seconds <= (15 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds,
    (project IS NULL) AS project_missing,
    (language IS NULL) AS language_missing
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
