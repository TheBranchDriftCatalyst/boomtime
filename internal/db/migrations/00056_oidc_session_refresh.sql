-- +goose Up
-- +goose StatementBegin

-- gaka-93f.11.6: store the provider refresh_token RECOVERABLY (AES-256-GCM,
-- BOOM_ENCRYPTION_KEY) instead of hashed, so /auth/refresh_token can silently
-- do a refresh-grant against the IdP and rotate the OIDC web session. The old
-- hashed_refresh column could only VERIFY a presented token, never USE one, so
-- a short id_token forced a full re-login every ~10 min. Additive + nullable:
-- OIDC is inert in prod (BOOM_AUTH_PROVIDER=local), so no existing rows carry a
-- usable refresh anyway. hashed_refresh is left in place (unused, harmless).
ALTER TABLE public.oidc_sessions ADD COLUMN IF NOT EXISTS encrypted_refresh bytea;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.oidc_sessions DROP COLUMN IF EXISTS encrypted_refresh;
-- +goose StatementEnd
