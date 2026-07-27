-- +goose Up
-- +goose StatementBegin

-- gaka-vh8: git-history backfill support.
--
-- DEVIATION FROM PLAN: the plan said "tag via sender='backfill:git'" but
-- heartbeats.sender has an FK to users(username) that must not point at a
-- non-user string. Instead we add a nullable `source` column: for real
-- Wakatime heartbeats it stays NULL (existing behavior), for backfilled
-- rows it holds the source tag ("backfill:git", "backfill:git:2026-Q3",
-- etc). The `sender` column continues to hold the owner's username on
-- every row, so gap/rollup/derived paths keep working unchanged. This
-- keeps the FK healthy AND gives us a clean per-source scope for the
-- overlap check, danger-zone delete, and admin stats.
--
-- What this migration adds:
--   1. A nullable `source` text column on heartbeats. NULL = real
--      Wakatime data; non-NULL = something we can safely purge / audit
--      independently. Backfill runs write the source_tag verbatim.
--   2. A partial btree index on (sender, source, time_sent) restricted
--      to non-NULL sources. Used by the "how many backfill rows do I
--      have?" stat + the DELETE FROM heartbeats WHERE source LIKE
--      'backfill:%' scrub path so the danger-zone purge doesn't full-
--      scan the heartbeats table.
--   3. A backfill_config table — one row per user, holds the tunables
--      the admin UI edits (cluster gap, lead/tail, HB rate, author
--      allowlist, source tag prefix, per-extension language overrides).
--      Referenced by the CLI on startup to inherit server-side defaults
--      before applying any --flag overrides.
--
-- FK on backfill_config.username -> users.username (text PK).

ALTER TABLE public.heartbeats
  ADD COLUMN IF NOT EXISTS source text;

CREATE INDEX IF NOT EXISTS heartbeats_backfill_source_idx
  ON public.heartbeats (sender, source, time_sent)
  WHERE source IS NOT NULL;

-- Note on idempotency: the existing constraint unique_heartbeats
-- (entity, sender, time_sent) already prevents duplicate rows for the
-- same (owner, entity, timestamp). The CLI's Materialize step is
-- deterministic for a given cluster config, so rerunning the same repo
-- with the same config is a no-op. No extra unique index needed. On
-- CONFLICT the existing insert_heartbeat.sql updates `machine` — we
-- extend that in the same migration to also update `source` so a
-- previously-real row that gets re-ingested as backfill (should not
-- happen, but be defensive) does not lose the tag.

CREATE TABLE public.backfill_config (
  username             text PRIMARY KEY REFERENCES public.users(username) ON DELETE CASCADE,
  cluster_gap_sec      integer NOT NULL DEFAULT 1800,   -- 30min
  pre_commit_lead_sec  integer NOT NULL DEFAULT 900,    -- 15min
  post_commit_tail_sec integer NOT NULL DEFAULT 300,    -- 5min
  heartbeat_rate_sec   integer NOT NULL DEFAULT 120,    -- 2min
  author_emails        text[]  NOT NULL DEFAULT '{}',
  source_tag           text    NOT NULL DEFAULT 'backfill:git',
  lang_map             jsonb   NOT NULL DEFAULT '{}'::jsonb, -- ext -> lang overrides
  updated_at           timestamp with time zone NOT NULL DEFAULT now()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public.backfill_config;
DROP INDEX IF EXISTS public.heartbeats_backfill_source_idx;

-- +goose StatementEnd
