-- Phase A: today's total = SUM of precomputed gap_seconds within the 15-min
-- limit. $1 sender.
SELECT
    coalesce(CAST(SUM(CASE WHEN gap_seconds <= (15 * 60) THEN gap_seconds ELSE 0 END) AS bigint), 0) AS total_time
FROM
    heartbeats
WHERE
    sender = $1
    AND time_sent >= (current_date + interval '0' day)
    AND time_sent < (current_date + interval '1' day)
