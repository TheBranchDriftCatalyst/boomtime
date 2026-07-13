-- +goose Up

-- gaka-o4m: Space-scoped dashboard queries were seq-scanning the whole
-- sender's heartbeats when a Space carried any regex membership rule
-- (project ~ $n). On wide date ranges — sticky since the range-persistence
-- change — 23 dashboard aggregations per load × 5.5s each = a very bad time.
--
-- Fix (option C stacked with option B):
--
--   1. pg_trgm GIN indexes on project + branch let the planner pre-narrow
--      via bitmap scan for BOTH `col ~ pattern` and `col LIKE 'lit%'`
--      predicates, so an unanchored space rule (e.g. `project ~ 'teak'`)
--      still gets a fast filter path even in the worst-case all-time range.
--      pg_trgm has been shipped with Postgres since 9.1 and is trusted; no
--      superuser hoop.
--
--   2. text_pattern_ops btree on project accelerates anchored `^literal`
--      rules that the Go layer rewrites to `LIKE 'literal%'` (see
--      spaces.go anchoredLiteralPrefix). Necessary when the DB collation
--      isn't C-compatible — the default btree only serves LIKE 'prefix%'
--      when the operator class is text_pattern_ops.
--
-- Storage cost is modest (roughly 30–80MB per GIN on 400–500k rows) and
-- write cost is a few extra microseconds per heartbeat insert. Both are
-- acceptable for the query wins the ticket documents.
--
-- entity is intentionally NOT indexed here — it's per-file with the highest
-- cardinality on the table, so a trigram index would balloon. Add it in a
-- follow-up if Space rules on entity become a real workload.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS heartbeats_project_trgm_idx
    ON heartbeats USING gin (project gin_trgm_ops);

CREATE INDEX IF NOT EXISTS heartbeats_branch_trgm_idx
    ON heartbeats USING gin (branch gin_trgm_ops);

CREATE INDEX IF NOT EXISTS heartbeats_project_pattern_idx
    ON heartbeats (project text_pattern_ops);

-- +goose Down
DROP INDEX IF EXISTS heartbeats_project_pattern_idx;
DROP INDEX IF EXISTS heartbeats_branch_trgm_idx;
DROP INDEX IF EXISTS heartbeats_project_trgm_idx;
-- pg_trgm is left installed — dropping the extension would fail if any
-- other index (in this or an adjacent database) still uses gin_trgm_ops.
