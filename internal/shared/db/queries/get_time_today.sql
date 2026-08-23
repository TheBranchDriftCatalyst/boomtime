-- boom-dg7: "today" bounded by user-local midnight ($2 IANA tz name) instead
-- of UTC midnight. Before the fix, an 11pm-PT status bar refresh saw the
-- next-day UTC bounds and reported 0 for the actual local day's coding.
--
-- Phase A: today's total = SUM of precomputed gap_seconds within the 15-min
-- limit. Bounds are the user's local midnight -> next midnight, converted
-- back to UTC wall clock (heartbeats.time_sent is `timestamp without time zone`
-- stored as UTC — see internal/db/ingest.go unixToTime — so the bounds must
-- also be naked UTC timestamps for the index-friendly comparison to line up).
-- Chain: now() -> local wall-clock via `AT TIME ZONE $2` -> ::date snaps to
-- local midnight -> `AT TIME ZONE $2` interprets that midnight as local ->
-- `AT TIME ZONE 'UTC'` yields the naked UTC wall clock the column stores.
-- $1 sender, $2 IANA tz name.
SELECT
    coalesce(CAST(SUM(CASE WHEN gap_seconds <= (15 * 60) THEN gap_seconds ELSE 0 END) AS bigint), 0) AS total_time
FROM
    heartbeats
WHERE
    sender = $1
    AND time_sent >= ((((now() AT TIME ZONE $2)::date) AT TIME ZONE $2) AT TIME ZONE 'UTC')
    AND time_sent < (((((now() AT TIME ZONE $2)::date) + interval '1' day) AT TIME ZONE $2) AT TIME ZONE 'UTC')
