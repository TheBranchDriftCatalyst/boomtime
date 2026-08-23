-- +goose Up
-- +goose StatementBegin

-- catalyst-go-jobs owner (boom-hney.6/.7): user-scoped jobs so completion
-- notifications route to the right person (e.g. an avatar-render job toasts its
-- owner). Empty string = a system job (e.g. github-stats-refresh) with no owner.
ALTER TABLE public.jobs ADD COLUMN IF NOT EXISTS owner text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS jobs_owner_idx ON public.jobs (owner, created_at DESC) WHERE owner <> '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.jobs_owner_idx;
ALTER TABLE public.jobs DROP COLUMN IF EXISTS owner;
-- +goose StatementEnd
