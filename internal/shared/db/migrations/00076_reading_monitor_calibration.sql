-- +goose Up
-- +goose StatementBegin

-- Reading-monitor CALIBRATION window (catalyst-books §5.1, PART 2). Normal L1/L2
-- polling (60–120s) ALIASES the sub-60s whispersync push cadence — you cannot
-- measure a cadence by sampling slower than it — so the optimal-timing
-- recommendation needs a temporary HIGH-FIDELITY burst. This per-user timestamp is
-- that window's expiry: while now < reading_monitor_calibrating_until the engine
-- polls EVERY in-progress book at the fast CalibrationInterval (default 10s)
-- regardless of L1/L2, then auto-reverts when it passes (no manual step).
--
-- Kept on the users row next to reading_monitor_enabled / reading_monitor_mode
-- (migration 00072) so the whole per-user monitor control surface lives together
-- and cascade-deletes with the user. NULL = not calibrating (the default).
ALTER TABLE public.users
    ADD COLUMN IF NOT EXISTS reading_monitor_calibrating_until timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS reading_monitor_calibrating_until;
-- +goose StatementEnd
