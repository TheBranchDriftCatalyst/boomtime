-- +goose Up
-- +goose StatementBegin

-- Continuous-progress push dedup (gaka): remember the percent WE last actually
-- pushed to Hardcover for an in-progress reading_item so the forward sync only
-- re-pushes when the local progress has moved since. NULL = never pushed (the
-- backlog the first non-dry-run cycle flushes). This kills the per-sync
-- re-match+re-push flood where every in-progress book paid the rate-limited
-- match ladder on every run even though its % had not changed.
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS hardcover_pushed_progress integer;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS hardcover_pushed_progress;
-- +goose StatementEnd
