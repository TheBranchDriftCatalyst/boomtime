-- +goose Up
-- +goose StatementBegin

-- Per-user GitHub OAuth-App connection (boom-2ip Phase 1). Mirrors the
-- encrypted-at-rest shape of the Wakatime key columns (migrations 00023/00024):
--
--   encrypted_github_token  — AES-256-GCM ciphertext of the user's GitHub OAuth
--                             access token (nonce||ct||tag; see
--                             internal/auth/crypto.go). NEVER plaintext.
--   github_token_status     — 'valid' | 'invalid' | 'unknown' last-known probe
--                             result (GET https://api.github.com/user at connect
--                             time). NULL when no token is stored.
--   github_token_checked_at — wall-clock of the last status write. NULL = never.
--   github_login            — the GitHub login (@handle) captured at connect
--                             time so the Settings card can render "Connected as
--                             @login" WITHOUT ever decrypting the token. NULL
--                             when no token is stored.
--
-- The token itself is a Secret whose confidentiality rests entirely on
-- BOOM_ENCRYPTION_KEY (same threat model as encrypted_wakatime_key). The
-- rotate-encryption-key command re-encrypts this column alongside the Wakatime
-- one in a single transaction (cmd/boomtime/rotate.go).
ALTER TABLE public.users
    ADD COLUMN encrypted_github_token  bytea,
    ADD COLUMN github_token_status     text,
    ADD COLUMN github_token_checked_at timestamp with time zone,
    ADD COLUMN github_login            text;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS encrypted_github_token,
    DROP COLUMN IF EXISTS github_token_status,
    DROP COLUMN IF EXISTS github_token_checked_at,
    DROP COLUMN IF EXISTS github_login;
-- +goose StatementEnd
