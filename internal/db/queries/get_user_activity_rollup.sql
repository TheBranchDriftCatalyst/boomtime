-- Fast path for the Overview stats at the default 15-min limit: read the
-- pre-aggregated hb_rollup_daily instead of scanning raw heartbeats. branch and
-- entity are placeholders ('Other') since the stats payload doesn't break down by
-- them (segmentStat uses project/language/editor/platform/machine only).
-- $1 sender, $2 start, $3 end.
WITH stats AS (
    SELECT
        day + interval '0h' AS day,
        project,
        language,
        editor,
        'Other'::text AS branch,
        platform,
        machine,
        'Other'::text AS entity,
        total_seconds
    FROM
        hb_rollup_daily
    WHERE
        sender = $1
        AND day >= $2::date
        AND day <= $3::date
    ORDER BY
        day
)
SELECT
    *,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats
