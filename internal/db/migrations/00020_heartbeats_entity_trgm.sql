-- +goose Up

-- gaka-o4m follow-up: add pg_trgm GIN on entity too. 00019 deferred this on
-- cardinality grounds (entity is per-file — highest-cardinality column on
-- the table), but a Space rule on entity file paths falls back to the raw
-- Seq Scan without it, same as project used to. The storage cost is real
-- (this is the biggest text column on the table), but it's a one-time write
-- and the wins on entity-scoped Spaces mirror what 00019 unlocked for
-- project-scoped ones.
--
-- pg_trgm is already installed by 00019; the extension line is idempotent.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS heartbeats_entity_trgm_idx
    ON heartbeats USING gin (entity gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS heartbeats_entity_trgm_idx;
