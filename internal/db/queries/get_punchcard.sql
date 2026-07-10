-- Day-of-week x hour-of-day coding intensity (a "punchcard"). Times are UTC —
-- dow/hour are extracted in UTC, so the FE must document the tz caveat.
-- dow: 0=Sunday .. 6=Saturday (Postgres EXTRACT(DOW)). hour: 0..23.
-- Gap-conditional SUM over precomputed gap_seconds; excludes hidden projects via
-- an appended `AND NOT (project = ANY($n))` after the range-end anchor.
-- $1 sender, $2 start, $3 end, $4 limit (minutes).
SELECT
    CAST(extract(dow FROM time_sent) AS int) AS dow,
    CAST(extract(hour FROM time_sent) AS int) AS hour,
    CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS seconds
FROM
    heartbeats
WHERE
    sender = $1
    AND time_sent >= $2
    AND time_sent <= $3
GROUP BY
    extract(dow FROM time_sent),
    extract(hour FROM time_sent)
ORDER BY
    dow,
    hour;
