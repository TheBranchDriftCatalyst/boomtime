-- +goose Up
-- +goose StatementBegin

-- catalyst-go-jobs (gaka-hney.1): a generic, DB-backed job queue + periodic
-- scheduler, additive alongside the RabbitMQ image path (nothing here touches
-- imagejobs). `jobs` is the work queue — one row per unit of work, claimed by
-- workers via FOR UPDATE SKIP LOCKED so many workers never grab the same row.
-- `job_schedules` drives periodic enqueues; the scheduler advances a row with
-- an atomic UPDATE ... RETURNING, making "who enqueues" leader-singleton even
-- across replicas.
CREATE TABLE IF NOT EXISTS public.jobs (
    id           bigserial   PRIMARY KEY,
    kind         text        NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status       text        NOT NULL DEFAULT 'queued',  -- queued|running|done|failed
    attempts     integer     NOT NULL DEFAULT 0,
    max_attempts integer     NOT NULL DEFAULT 1,
    error        text        NOT NULL DEFAULT '',
    run_at       timestamptz NOT NULL DEFAULT now(),
    locked_by    text        NOT NULL DEFAULT '',
    locked_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    finished_at  timestamptz
);
-- Claim path: due, queued jobs oldest-first. Partial index stays tiny (only the
-- backlog), which is what the hot SELECT ... FOR UPDATE SKIP LOCKED scans.
CREATE INDEX IF NOT EXISTS jobs_claim_idx ON public.jobs (run_at) WHERE status = 'queued';
-- Admin listing (jobs S2): newest-per-kind.
CREATE INDEX IF NOT EXISTS jobs_kind_created_idx ON public.jobs (kind, created_at DESC);

CREATE TABLE IF NOT EXISTS public.job_schedules (
    kind             text        PRIMARY KEY,
    interval_seconds integer     NOT NULL,
    next_run_at      timestamptz NOT NULL DEFAULT now(),
    last_run_at      timestamptz
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.job_schedules;
DROP TABLE IF EXISTS public.jobs;
-- +goose StatementEnd
