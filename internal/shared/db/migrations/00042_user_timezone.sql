-- +goose Up
-- +goose StatementBegin
--
-- gaka-dg7: per-user IANA timezone for badge/streak/daily-rollup calculations.
--
-- Before this migration, every SQL that pulled dow/hour/date out of time_sent
-- did so in UTC. Result: a US-Pacific user's 22:00 local (06:00 UTC) never
-- triggers "late-night-coder" or NIGHT WATCH; day boundaries used by streaks
-- and daily rollups also cut wrong.
--
-- Column shape: NOT NULL DEFAULT '' so callers can distinguish the two
-- important states with a COALESCE(NULLIF(x, ''), fallback) idiom:
--   ''    -> user has NEVER picked an explicit timezone; the resolver falls
--           through to BOOM_DEFAULT_TIMEZONE (else 'UTC').
--   'X'   -> user made an explicit choice (or the FE auto-detect fired on
--           first login and persisted the browser's zone). Wins over any env
--           default.
--
-- Empty-string sentinel over NULL: SQL null-handling in the resolver requires
-- an extra IS NULL check; empty string collapses cleanly through NULLIF, and
-- the Go scan into `string` needs no *string dance.
--
-- Not choosing a hard 'UTC' default because that would prevent the operator's
-- BOOM_DEFAULT_TIMEZONE from ever winning for existing users (every row would
-- read as "explicitly UTC" and short-circuit the fallback chain).
ALTER TABLE public.users
  ADD COLUMN timezone TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users DROP COLUMN timezone;
-- +goose StatementEnd
