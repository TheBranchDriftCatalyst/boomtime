-- +goose Up
-- +goose StatementBegin

-- Remove the git-history backfill experiment (reverses 00037_backfill.sql).
--
-- The experiment (gaka-vh8) synthesized fake WakaTime heartbeats tagged
-- source='backfill:git' and stored per-user tunables in backfill_config. It
-- never graduated past an experiment and its whole apparatus (CLI `backfill
-- git`, the admin plane, the in-memory job registry, the db layer) has been
-- deleted in code. This migration removes its schema + data:
--
--   1. DELETE the synthetic heartbeats. Real ingest NEVER writes `source`
--      (insert_heartbeat.sql leaves it NULL), so `source IS NOT NULL` selects
--      exactly the experiment's fabricated rows. Leaving them behind after the
--      column drop would strand them as permanently-untaggable fake activity
--      polluting real stats. DESTRUCTIVE + IRREVERSIBLE (user-confirmed).
--   2. Drop the backfill-only partial index + the backfill_config table.
--   3. Drop the now-unused `source` column.

DELETE FROM public.heartbeats WHERE source IS NOT NULL;

DROP INDEX IF EXISTS public.heartbeats_backfill_source_idx;

DROP TABLE IF EXISTS public.backfill_config;

ALTER TABLE public.heartbeats DROP COLUMN IF EXISTS source;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse: recreate the schema exactly as 00037 left it (the synthetic rows
-- deleted on Up cannot be restored — they were the discarded experiment's
-- output).

ALTER TABLE public.heartbeats
  ADD COLUMN IF NOT EXISTS source text;

CREATE INDEX IF NOT EXISTS heartbeats_backfill_source_idx
  ON public.heartbeats (sender, source, time_sent)
  WHERE source IS NOT NULL;

CREATE TABLE IF NOT EXISTS public.backfill_config (
  username             text PRIMARY KEY REFERENCES public.users(username) ON DELETE CASCADE,
  cluster_gap_sec      integer NOT NULL DEFAULT 1800,
  pre_commit_lead_sec  integer NOT NULL DEFAULT 900,
  post_commit_tail_sec integer NOT NULL DEFAULT 300,
  heartbeat_rate_sec   integer NOT NULL DEFAULT 120,
  author_emails        text[]  NOT NULL DEFAULT '{}',
  source_tag           text    NOT NULL DEFAULT 'backfill:git',
  lang_map             jsonb   NOT NULL DEFAULT '{}'::jsonb,
  updated_at           timestamp with time zone NOT NULL DEFAULT now()
);

-- +goose StatementEnd
