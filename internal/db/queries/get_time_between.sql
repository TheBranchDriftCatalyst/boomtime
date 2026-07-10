-- Phase A: per (user, project, [min_date, max_date]) window, SUM precomputed
-- gap_seconds within the 15-min limit. Params: unnest($1,$2,$3,$4) =
-- (username, project_name, min_date, max_date).
WITH input_table AS (
    SELECT
        *
    FROM
        unnest($1, $2, $3, $4) AS input_table (username,
            project_name,
            min_date,
            max_date))
SELECT
    CAST(SUM(CASE WHEN gap_seconds <= (15 * 60) THEN gap_seconds ELSE 0 END) AS bigint)
FROM
    heartbeats,
    input_table
WHERE
    sender = input_table.username
    AND project = input_table.project_name
    AND time_sent > input_table.min_date
    AND time_sent < input_table.max_date
GROUP BY
    min_date,
    max_date
