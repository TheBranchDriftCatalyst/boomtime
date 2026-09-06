-- Phase A: per (user, project, [min_date, max_date]) window, SUM precomputed
-- gap_seconds within the 15-min limit. Params: unnest($1,$2,$3,$4) =
-- (username, project_name, min_date, max_date). Explicit ::type[] casts are
-- required because Postgres reports `function pg_catalog.unnest(unknown) is
-- not unique` (SQLSTATE 42725) when the arg types are inferred as `unknown`
-- and multiple `unnest` overloads exist for the multi-array form. See
-- boom-6yr for the failure history.
--
-- boom-gsnv: the result is ONE ROW PER INPUT WINDOW, in input order. Three
-- things make that true and all three are load-bearing for the sole caller
-- (stats/commits.go zips the result positionally onto its commit gaps):
--
--   1. WITH ORDINALITY tags every unnested window with its 1-based input
--      position, so identical (min_date, max_date) pairs stay distinct rows
--      instead of collapsing into one aggregate group.
--   2. LEFT JOIN (was: an implicit inner join `FROM heartbeats, input_table`)
--      keeps a window that matches zero heartbeats — a commit pushed from CI
--      or another machine, a docs-only commit — instead of dropping the row
--      and shifting every later window's total onto the wrong commit.
--   3. ORDER BY ordinality: GROUP BY may hash-aggregate, whose output order
--      is plan-dependent. Without this the rows come back in arbitrary order
--      and a positional zip is a permutation of the truth.
--
-- COALESCE guards the all-NULL side of the LEFT JOIN so an empty window
-- reports 0 rather than NULL (the caller scans into int64).
WITH input_table AS (
    SELECT
        *
    FROM
        unnest($1::text[], $2::text[], $3::timestamp[], $4::timestamp[])
        WITH ORDINALITY AS input_table (username,
            project_name,
            min_date,
            max_date,
            ordinality))
SELECT
    CAST(COALESCE(SUM(CASE WHEN heartbeats.gap_seconds <= (15 * 60) THEN heartbeats.gap_seconds ELSE 0 END), 0) AS bigint)
FROM
    input_table
    LEFT JOIN heartbeats ON heartbeats.sender = input_table.username
        AND heartbeats.project = input_table.project_name
        AND heartbeats.time_sent > input_table.min_date
        AND heartbeats.time_sent < input_table.max_date
GROUP BY
    input_table.ordinality
ORDER BY
    input_table.ordinality
