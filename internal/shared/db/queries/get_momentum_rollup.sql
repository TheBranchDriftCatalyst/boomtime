-- Fast path for Momentum at the default 15-min limit: read the pre-aggregated
-- hb_rollup_daily instead of scanning raw heartbeats. The rollup's `day` column
-- is already computed in the sender's TZ at ingest (boom-dg7), so
-- date_trunc('week', day) yields the same ISO Monday the raw path would in
-- user-local TZ. Rollup rows always have project = coalesce(raw project, 'Other')
-- so we filter null-project rows via the project_missing flag (boom-6ci) to
-- mirror the raw path's `AND project IS NOT NULL` — otherwise browsing time
-- (null project) would surface as a fake "Other" project bump.
--
-- $1 sender, $2 start, $3 end. (Limit is fixed at 15 minutes because the
-- rollup itself was built with a hardcoded gap_seconds <= 900 cutoff; the Go
-- handler falls back to raw for non-default limits.)
SELECT
    project,
    (date_trunc('week', day))::date AS week_start,
    CAST(sum(total_seconds) AS int8) AS total_seconds
FROM
    hb_rollup_daily
WHERE
    sender = $1
    AND day >= $2::date
    AND day <= $3::date
    AND NOT project_missing
GROUP BY
    project,
    date_trunc('week', day)
ORDER BY
    project,
    week_start;
