-- Fast path for the Overview stats at the default 15-min limit: read the
-- pre-aggregated hb_rollup_daily instead of scanning raw heartbeats. branch and
-- entity are placeholders ('Other') in the OUTPUT since the stats payload
-- doesn't break down by them (segmentStat uses project/language/editor/platform/
-- machine only). The rollup table itself now STORES category/plugin/branch too
-- (so hides and Space rules on those axes can splice as WHERE predicates before
-- the CTE GROUP BY collapses them back to the 5-axis output grain).
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
        CAST(sum(total_seconds) AS int8) AS total_seconds,
        -- boom-6ci: propagate the axis-missing flags. bool_and here is
        -- collapsing the same-axis-value rows down to the 5-axis output
        -- grain (get_user_activity_rollup drops branch/entity); the flag
        -- for a row is TRUE only if every underlying rollup row had it
        -- true. Branch/entity flags aren't projected (they're placeholders
        -- in this output shape).
        bool_and(project_missing) AS project_missing,
        bool_and(language_missing) AS language_missing,
        bool_and(editor_missing) AS editor_missing,
        bool_and(platform_missing) AS platform_missing,
        bool_and(machine_missing) AS machine_missing
    FROM
        hb_rollup_daily
    WHERE
        sender = $1
        AND day >= $2::date
        AND day <= $3::date
    GROUP BY
        day, project, language, editor, platform, machine
    ORDER BY
        day
)
SELECT
    day, project, language, editor, branch, platform, machine, entity, total_seconds,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (), 0) AS numeric(13, 12)), 0) AS pct,
    coalesce(CAST(1.0 * total_seconds / nullif (sum(total_seconds) OVER (PARTITION BY day), 0) AS numeric(13, 12)), 0) AS daily_pct,
    project_missing, language_missing, editor_missing,
    FALSE AS branch_missing, platform_missing, machine_missing
FROM
    stats
