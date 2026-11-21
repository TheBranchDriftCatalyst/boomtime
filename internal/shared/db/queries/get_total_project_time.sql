-- Phase A: total time on a project over the last $2 days = SUM of precomputed
-- gap_seconds within the default gap cutoff ($4, seconds — bound from
-- db.defaultGapCutoffSeconds). $1 sender, $2 days, $3 project, $4 cutoff.
SELECT
    CAST(coalesce(sum(CASE WHEN gap_seconds <= $4 THEN gap_seconds ELSE 0 END), 0) AS bigint)
FROM
    heartbeats
WHERE
    sender = $1
    AND project = $3
    AND time_sent >= (now() - interval '1' day * $2)
    AND time_sent < now()
