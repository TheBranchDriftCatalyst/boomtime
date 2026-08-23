-- boom-dg7: week boundary computed in user-local TZ ($5) — Postgres'
-- date_trunc('week', X) yields the ISO Monday of X's timezone, so we shift
-- into the user's TZ first. Fixes momentum weekly bumps drifting across a
-- Sunday-in-UTC boundary for west-coast users.
--
-- Weekly coding time per project (for a project-momentum stream/bump chart).
-- Gap-conditional SUM over precomputed gap_seconds. Excludes hidden projects via
-- `AND NOT (project = ANY($n))` after the range-end anchor. The Go side selects
-- the top-N projects by total and gap-fills the week series.
-- $1 sender, $2 start, $3 end, $4 limit (minutes), $5 IANA tz name.
-- boom-6ci: momentum is a per-project chart, so null-project heartbeats
-- (browser sessions with no project context) shouldn't create a fake
-- "Other" project bump. Filter before the aggregation. Coalesce becomes a
-- no-op but kept for defense-in-depth against a future refactor loosening
-- the WHERE.
SELECT
    coalesce(project, 'Other') AS project,
    (date_trunc('week', (time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5))::date AS week_start,
    CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
FROM
    heartbeats
WHERE
    sender = $1
    AND time_sent >= $2
    AND time_sent <= $3
    AND project IS NOT NULL
GROUP BY
    coalesce(project, 'Other'),
    date_trunc('week', (time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)
ORDER BY
    project,
    week_start;
