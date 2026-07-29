-- gaka-dg7: session_day bucket computed in user-local TZ ($5). A late-evening
-- Pacific session that ran 21:30–00:30 PT would have been split across two
-- session_day rows in UTC (dates 04:30 -> 07:30 UTC = same UTC day, but a
-- session ending 23:59 UTC / bumping into +1 day in UTC only was one case).
-- More importantly: for a US user the "day the session belongs to" reads
-- naturally in their local clock, not a UTC date the FE has to relabel.
--
-- Sessionize a sender's heartbeats: ordered by time_sent, a NEW session starts
-- when gap_seconds IS NULL or > limit*60 (the "break"). A session's duration is
-- the SUM of its in-session gaps (gaps that are NOT breaks). The break gap itself
-- is excluded (it's the idle time between sessions).
-- Returns one row per session: its start day (user-local) and total seconds.
-- Excludes hidden projects via `AND NOT (project = ANY($n))` after the range-end
-- anchor. $1 sender, $2 start, $3 end, $4 limit (minutes), $5 IANA tz name.
WITH ordered AS (
    SELECT
        time_sent,
        gap_seconds,
        (gap_seconds IS NULL OR gap_seconds > ($4 * 60)) AS is_break
    FROM
        heartbeats
    WHERE
        sender = $1
        AND time_sent >= $2
        AND time_sent <= $3
),
tagged AS (
    SELECT
        time_sent,
        gap_seconds,
        is_break,
        sum(CASE WHEN is_break THEN 1 ELSE 0 END) OVER (ORDER BY time_sent) AS session_id
    FROM
        ordered
)
SELECT
    ((min(time_sent) AT TIME ZONE 'UTC') AT TIME ZONE $5)::date AS session_day,
    CAST(sum(CASE WHEN is_break THEN 0 ELSE coalesce(gap_seconds, 0) END) AS int8) AS session_seconds
FROM
    tagged
GROUP BY
    session_id
ORDER BY
    session_day;
