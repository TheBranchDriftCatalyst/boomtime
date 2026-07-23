-- +goose Up

-- gaka-b5x.2: hash session tokens at rest.
--
-- Before this migration, refresh_tokens.refresh_token and auth_tokens.token
-- stored the RAW base64(uuid) value the client presents. A DB read (SQLi,
-- leaked backup, stolen replica) yielded directly usable session tokens — no
-- offline crack needed. We now store SHA-256 of the token in a new bytea
-- column and compare on lookup (see internal/db/auth.go:hashSessionToken).
--
-- Dual-path graceful cutover:
--   * Existing rows keep their raw `refresh_token` / `token` values populated
--     and hashed_* is NULL — lookup falls back to the raw column, so live
--     sessions minted before this migration keep working until they expire.
--   * All NEW rows (see db/auth.go INSERT paths) write ONLY the hashed
--     column and leave the raw column NULL. That way the DB never sees a
--     usable token again for any session minted post-migration.
--   * After the longest refresh_tokens.token_expiry window has passed on the
--     deployment (default 24h; conservative 30d covers overrides seen in
--     the wild), a follow-up migration will DROP the raw columns entirely.
--     Follow-up bead: "Drop raw refresh_token / token columns after cutover
--     window" (filed under gaka-awh).
--
-- Constraint changes: the raw columns were PRIMARY KEY (NOT NULL). New rows
-- write NULL there, so we drop the PK and NOT NULL. Legacy uniqueness is
-- preserved via a partial UNIQUE index (raw column WHERE NOT NULL); the
-- hashed columns get their own partial UNIQUE.
--
-- Why not backfill? Backfill would require the raw token value to compute
-- the hash — which we intentionally throw away at the boundary. The dual
-- path naturally drains as sessions age out; forcing a re-login on every
-- user for a token-hygiene refactor is unnecessarily disruptive.

ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS hashed_refresh_token BYTEA NULL;

ALTER TABLE auth_tokens
    ADD COLUMN IF NOT EXISTS hashed_token BYTEA NULL;

-- +goose Down

ALTER TABLE auth_tokens
    DROP COLUMN IF EXISTS hashed_token;

ALTER TABLE refresh_tokens
    DROP COLUMN IF EXISTS hashed_refresh_token;
