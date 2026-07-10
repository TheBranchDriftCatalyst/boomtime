-- Weekly coding time per project (for a project-momentum stream/bump chart).
-- date_trunc('week', ...) yields the ISO Monday week-start (UTC). Gap-conditional
-- SUM over precomputed gap_seconds. Excludes hidden projects via
-- `AND NOT (project = ANY($n))` after the range-end anchor. The Go side selects
-- the top-N projects by total and gap-fills the week series.
-- $1 sender, $2 start, $3 end, $4 limit (minutes).
SELECT
    coalesce(project, 'Other') AS project,
    (date_trunc('week', time_sent))::date AS week_start,
    CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS total_seconds
FROM
    heartbeats
WHERE
    sender = $1
    AND time_sent >= $2
    AND time_sent <= $3
GROUP BY
    coalesce(project, 'Other'),
    date_trunc('week', time_sent)
ORDER BY
    project,
    week_start;
