-- +goose Up
-- +goose StatementBegin

-- Per-user Hardcover API bearer token (catalyst-books push target). Hardcover
-- is the SYNC TARGET: boomtime mirrors reading state out to it via its GraphQL
-- write API. Auth is a user-pasted bearer token (from Hardcover account
-- settings) that expires yearly + resets every Jan 1 — so a re-paste is a
-- routine event, not an error. Modeled 1:1 on 00048_github_token.sql /
-- 00057_amazon_device.sql (same per-user-secret shape + status columns).
--
--   encrypted_hardcover_key  — AES-256-GCM ciphertext of the bearer token,
--                              sealed under BOOM_ENCRYPTION_KEY. Registered in
--                              internal/domains/registry.go so
--                              rotate-encryption-key re-encrypts it and the DB
--                              backup includes it — automatically.
--   hardcover_key_status     — 'valid' | 'invalid' | 'unknown' last-known probe.
--                              Flipped to 'invalid' on a Hardcover 401 (the
--                              Jan-1 reset makes this routine — the UI prompts a
--                              re-paste).
--   hardcover_key_checked_at — wall-clock of the last status write (NULL = never)
ALTER TABLE public.users
    ADD COLUMN IF NOT EXISTS encrypted_hardcover_key  bytea,
    ADD COLUMN IF NOT EXISTS hardcover_key_status     text,
    ADD COLUMN IF NOT EXISTS hardcover_key_checked_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS encrypted_hardcover_key,
    DROP COLUMN IF EXISTS hardcover_key_status,
    DROP COLUMN IF EXISTS hardcover_key_checked_at;
-- +goose StatementEnd
