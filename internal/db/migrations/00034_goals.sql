-- +goose Up
-- +goose StatementBegin

-- gaka-wpb: goals — composite predicate-tree targets with cached progress.
--
-- One row per (owner, name). `spec` holds the recursive predicate tree as
-- opaque JSONB (see internal/stats/goals.go for the validated shape).
-- `last_progress` caches the most recent evaluation output; the read path
-- reuses it when `last_evaluated_at` is within the freshness window
-- (goalCacheTTL, see stats/goals.go). Heartbeat ingest + spec updates
-- clear last_progress so the next read recomputes.
--
-- uuid_generate_v4() from uuid-ossp (installed in the baseline) — mirrors
-- dashboard_layouts / widget_defs / badges rather than gen_random_uuid
-- (pgcrypto is not guaranteed to be present on older dev DBs).
CREATE TABLE public.goals (
    id                UUID PRIMARY KEY DEFAULT public.uuid_generate_v4(),
    owner             TEXT NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    description       TEXT,
    spec              JSONB NOT NULL,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_evaluated_at TIMESTAMPTZ,
    last_progress     JSONB,
    UNIQUE (owner, name)
);

-- Most reads scope to enabled goals only (dashboard batch endpoint,
-- ingest-side invalidation). Partial index keeps the hot lookup path cheap.
CREATE INDEX goals_owner_enabled ON public.goals(owner) WHERE enabled = true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.goals;
-- +goose StatementEnd
