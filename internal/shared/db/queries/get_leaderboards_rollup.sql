-- Fast path for Leaderboards: read the pre-aggregated hb_rollup_daily instead
-- of scanning raw heartbeats. The raw query uses a hardcoded gap_seconds <=
-- (15 * 60) filter, which is exactly what the rollup captured at ingest
-- (workout_duration_s OR gap_seconds <= 900), so summing rollup total_seconds
-- reproduces the raw sum byte-for-byte at the 15-min limit.
--
-- gaka-6ci: project_missing / language_missing propagate the raw-NULL
-- discriminator so ToLeaderboardsPayload can build per-language sub-
-- leaderboards WITHOUT the 'Other' bucket (browser tabs / AI console with no
-- language). The GLOBAL leaderboard still sums over every row so
-- total-time-per-user stays honest. The rollup already stores per-axis missing
-- flags per-row; bool_and here collapses same-project+language+sender rows
-- across days into ONE row whose flag is TRUE iff every underlying rollup
-- row had it TRUE.
-- $1 start, $2 end.
SELECT
    project,
    language,
    sender,
    CAST(sum(total_seconds) AS int8) AS total_seconds,
    bool_and(project_missing) AS project_missing,
    bool_and(language_missing) AS language_missing
FROM
    hb_rollup_daily
WHERE
    day >= $1::date
    AND day <= $2::date
GROUP BY
    project,
    language,
    sender
ORDER BY
    language
