-- Fast path for the CategoryDaily stats at the default 15-min limit: read the
-- pre-aggregated hb_rollup_daily instead of scanning raw heartbeats. The
-- rollup stores category (gaka-6ci) with its `_missing` sentinel, so we can
-- filter out null-category rows (`AND NOT category_missing`) to mirror the
-- raw path's `AND category IS NOT NULL` — a browser plugin or AI console tab
-- whose category wasn't set shouldn't surface as a fake 'Other' category on
-- the category pie.
--
-- gaka-dg7: the rollup's `day` column is already computed in the sender's TZ
-- at ingest, so the per-day category buckets align with what the raw path
-- produces in user-local TZ (no $tz bind needed here).
-- $1 sender, $2 start, $3 end.
WITH stats AS (
    SELECT
        day + interval '0h' AS day,
        category,
        CAST(sum(total_seconds) AS int8) AS total_seconds
    FROM
        hb_rollup_daily
    WHERE
        sender = $1
        AND day >= $2::date
        AND day <= $3::date
        AND NOT category_missing
    GROUP BY
        day, category
    ORDER BY
        day
)
SELECT
    day,
    category,
    total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct
FROM
    stats;
