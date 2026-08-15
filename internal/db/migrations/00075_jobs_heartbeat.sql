-- +goose Up
-- +goose StatementBegin

-- Jobs heartbeat + stale-job reaper (catalyst-go-jobs reliability). When a pod
-- restarts (every deploy), in-flight rows are left status='running' forever: they
-- hang in the admin UI, inflate the running count, and BLOCK the per-kind
-- concurrency cap so the scheduler's queued work piles up unbounded. The limiter's
-- slot TTL-frees the semaphore, but nothing reclaims the DB row.
--
-- heartbeat_at is refreshed by the executing worker (~every 30s) while a handler
-- runs; a reaper reclaims any running row whose heartbeat went stale (> lease TTL,
-- default 120s) — its worker died. Existing running rows have heartbeat_at NULL,
-- so the reaper's staleness check COALESCEs onto locked_at/started_at, letting the
-- CURRENT zombie backlog get reclaimed on the first post-deploy boot.
ALTER TABLE public.jobs ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz;

-- The reaper scans only running rows (bounded by the concurrency caps), so a tiny
-- partial index keeps that sweep off the full table.
CREATE INDEX IF NOT EXISTS jobs_running_idx ON public.jobs (id) WHERE status = 'running';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS jobs_running_idx;
ALTER TABLE public.jobs DROP COLUMN IF EXISTS heartbeat_at;
-- +goose StatementEnd
