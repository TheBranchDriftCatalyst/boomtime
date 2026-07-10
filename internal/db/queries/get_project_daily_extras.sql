-- Per-day authoring/reading split and distinct-file breadth for one project.
-- is_write only carries a write signal for ty='file' rows (domain/app have none),
-- so this is filtered to ty='file'. Uses the precomputed gap_seconds with the
-- same gap-conditional-SUM pattern as get_projects_stats.sql.
-- $1 sender, $2 project, $3 start, $4 end, $5 limit (minutes).
SELECT
    time_sent::date + interval '0h' AS day,
    CAST(sum(CASE WHEN gap_seconds <= ($5 * 60) AND is_write IS TRUE THEN gap_seconds ELSE 0 END) AS int8) AS write_seconds,
    CAST(sum(CASE WHEN gap_seconds <= ($5 * 60) AND is_write IS NOT TRUE THEN gap_seconds ELSE 0 END) AS int8) AS read_seconds,
    CAST(count(DISTINCT entity) AS int8) AS distinct_entities
FROM
    heartbeats
WHERE
    sender = $1
    AND project = $2
    AND ty = 'file'
    AND time_sent >= $3
    AND time_sent <= $4
GROUP BY
    time_sent::date + interval '0h'
ORDER BY
    day;
