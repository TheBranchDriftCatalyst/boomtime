-- +goose Up

-- gaka-6jm.2 (status extension): track the last-known validity of the saved
-- Wakatime API key so the FE can render a status dot without re-probing
-- wakatime.com on every render.
--
-- wakatime_key_status values:
--   'valid'    — the last probe (either the /users/current pre-save check,
--                or a completed import run) returned a 2xx from wakatime.com.
--   'invalid'  — the last probe returned 401/403 (wakatime rejected the key).
--   'unknown'  — reserved; the key is saved but we haven't validated it
--                against wakatime yet. Not currently written by the server
--                (save-time validation is mandatory), but the column allows
--                for a future soft-save-then-validate flow.
--   NULL       — no saved key.
--
-- wakatime_key_checked_at is set to now() every time the status is updated.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS wakatime_key_status TEXT NULL;
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS wakatime_key_checked_at TIMESTAMPTZ NULL;

-- +goose Down

ALTER TABLE users
    DROP COLUMN IF EXISTS wakatime_key_checked_at;
ALTER TABLE users
    DROP COLUMN IF EXISTS wakatime_key_status;
