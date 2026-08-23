-- 00045_axis_missing_flags.sql (boom-6ci)
--
-- Add per-axis "was this originally NULL on the source heartbeat?" sentinel
-- columns to hb_rollup_daily. Fixes the semantic collision where NULL-axis
-- heartbeats (browser sessions with no file open, AI console tabs, plugin-less
-- clients) get COALESCE'd to the literal string 'Other' in ingest.go — which
-- visually collides with capWithOther's synthetic 'Other (N more)' aggregation
-- bucket, making a language pie report 55%+ 'Other' when the real answer is
-- "half your tracked time was browsing."
--
-- Design (per boom-6ci):
--   - Additive columns, all NOT NULL DEFAULT FALSE. No PK change, no dedupe
--     hazard (Postgres NULL != NULL in composite unique keys, so allowing
--     NULL on the axis columns themselves is out — see the schema-audit
--     bead for rejection rationale).
--   - Ingest (internal/db/ingest.go) populates via `bool_or(<col> IS NULL)`
--     over each rollup group. See phase 2 of boom-6ci.
--   - Per-chart discriminators live in the 8 aggregation SQL files (phase 3
--     of boom-6ci). Language pie / project pie / file breakdown add
--     `WHERE NOT <axis>_missing`; category pie / total-time / editor pie
--     do not (browsing IS a category, browsers ARE editors, total must
--     reconcile).
--
-- Backfill note: existing rows keep <axis>_missing = FALSE. As users add new
-- heartbeats and their historical days get re-rolled (ingest.go DELETEs then
-- INSERTs on rollup rebuild), the flags self-heal. An operator can also run
-- `boomtime rebuild-rollup-flags` (boom-6ci.6) to force immediate correction.

-- +goose Up
ALTER TABLE hb_rollup_daily
  ADD COLUMN language_missing BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN project_missing  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN editor_missing   BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN platform_missing BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN machine_missing  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN category_missing BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN plugin_missing   BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN branch_missing   BOOLEAN NOT NULL DEFAULT FALSE;

-- Partial index for the hottest discriminator (language pie/heatmap/leaderboard).
-- Others can be added later if the query planner shows they're needed. The
-- (sender, day) prefix matches the existing hb_rollup_daily_sender_day_idx
-- shape so downstream queries can either path depending on selectivity.
CREATE INDEX IF NOT EXISTS hb_rollup_daily_language_present_idx
  ON hb_rollup_daily (sender, day)
  WHERE NOT language_missing;

-- +goose Down
DROP INDEX IF EXISTS hb_rollup_daily_language_present_idx;
ALTER TABLE hb_rollup_daily
  DROP COLUMN IF EXISTS language_missing,
  DROP COLUMN IF EXISTS project_missing,
  DROP COLUMN IF EXISTS editor_missing,
  DROP COLUMN IF EXISTS platform_missing,
  DROP COLUMN IF EXISTS machine_missing,
  DROP COLUMN IF EXISTS category_missing,
  DROP COLUMN IF EXISTS plugin_missing,
  DROP COLUMN IF EXISTS branch_missing;
