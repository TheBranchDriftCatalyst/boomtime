-- +goose Up

-- gaka-6jm.2: Encrypted-at-rest storage for a user's imported Wakatime API key.
--
-- Payload layout (see internal/auth/crypto.go): 12-byte AES-256-GCM nonce
-- concatenated with the ciphertext (which includes the trailing 16-byte GCM
-- auth tag). The symmetric key is loaded lazily on first Encrypt/Decrypt from
-- the BOOM_ENCRYPTION_KEY env var (base64-encoded 32 bytes). Server startup
-- logs a WARNING when the env is unset so this column simply remains NULL
-- until a real key is configured.
--
-- NULL means "no saved key for this user yet". The plaintext key is never
-- persisted anywhere, never logged, and never returned via the API surface —
-- see internal/handler/wakatime_key.go for the safe read side that only ever
-- reports {"hasSavedKey": bool}.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS encrypted_wakatime_key BYTEA NULL;

-- +goose Down

ALTER TABLE users
    DROP COLUMN IF EXISTS encrypted_wakatime_key;
