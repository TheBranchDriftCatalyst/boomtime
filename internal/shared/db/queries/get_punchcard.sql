-- gaka-dg7: hour/dow extracted in user-local TZ (from users.timezone +
-- BOOM_DEFAULT_TIMEZONE resolver — see internal/handler/timezone.go). Before
-- this fix, a Pacific user's 22:00 local (06:00 UTC) never triggered
-- late-night-coder / NIGHT WATCH archetypes because the bucket said 06.
--
-- Day-of-week x hour-of-day coding intensity (a "punchcard"). The FE reads
-- dow/hour as if they were user-local; the server enforces that by extracting
-- from ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5).
-- dow: 0=Sunday .. 6=Saturday (Postgres EXTRACT(DOW)). hour: 0..23.
-- Gap-conditional SUM over precomputed gap_seconds; excludes hidden projects via
-- an appended `AND NOT (project = ANY($n))` after the range-end anchor.
-- $1 sender, $2 start, $3 end, $4 limit (minutes), $5 IANA tz name.
SELECT
    CAST(extract(dow FROM ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)) AS int) AS dow,
    CAST(extract(hour FROM ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)) AS int) AS hour,
    CAST(sum(CASE WHEN gap_seconds <= ($4 * 60) THEN gap_seconds ELSE 0 END) AS int8) AS seconds
FROM
    heartbeats
WHERE
    sender = $1
    AND time_sent >= $2
    AND time_sent <= $3
GROUP BY
    extract(dow FROM ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5)),
    extract(hour FROM ((time_sent AT TIME ZONE 'UTC') AT TIME ZONE $5))
ORDER BY
    dow,
    hour;
