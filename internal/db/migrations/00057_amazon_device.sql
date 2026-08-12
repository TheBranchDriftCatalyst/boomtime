-- +goose Up
-- +goose StatementBegin

-- Shared Amazon device credential (catalyst-books + catalyst-audiobooks).
-- Audible AND Kindle authenticate off ONE Amazon device registration
-- (adp_token + RSA private key); we store it ONCE here, encrypted, and both
-- ingestion domains sign requests with it. Modeled 1:1 on 00048_github_token.sql
-- (users.encrypted_github_token) — same per-user-secret shape + status columns.
--
--   encrypted_amazon_device  — AES-256-GCM ciphertext of the device auth blob
--                              (adp_token + RSA key + refresh token), sealed
--                              under BOOM_ENCRYPTION_KEY. Registered in
--                              internal/domains/registry.go so rotate-encryption-key
--                              re-encrypts it and the DB backup includes it —
--                              automatically, by construction.
--   amazon_device_status     — 'valid' | 'invalid' | 'unknown' last-known probe
--   amazon_device_checked_at — wall-clock of the last status write (NULL = never)
ALTER TABLE public.users
    ADD COLUMN IF NOT EXISTS encrypted_amazon_device  bytea,
    ADD COLUMN IF NOT EXISTS amazon_device_status     text,
    ADD COLUMN IF NOT EXISTS amazon_device_checked_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS encrypted_amazon_device,
    DROP COLUMN IF EXISTS amazon_device_status,
    DROP COLUMN IF EXISTS amazon_device_checked_at;
-- +goose StatementEnd
