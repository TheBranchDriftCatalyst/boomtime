-- +goose Up
-- +goose StatementBegin

-- Belt-and-suspenders CHECK constraint: an auth_tokens row MUST have either a
-- raw token (legacy pre-v26 path) OR a hashed_token (new path shipped in v26).
-- A row with both NULL is unreachable by /auth/access_token lookup AND
-- unreachable by the /api/v1/users/current/tokens/:id/revoke handler — it
-- shows up as a ghost in the UI that can't be deleted.
--
-- Root cause discovered in production 2026-07-23: a single panda-owned row
-- with both columns NULL slipped past the v27 lookup-relaxation. Likely came
-- from a CLI create-token path that raced with the v26 migration cutover.
--
-- Nothing in the app writes a row like this today (both InsertAPIToken and
-- CreateAccessTokens always populate hashed_token), so the constraint should
-- validate cleanly. If any legacy ghost rows exist, callers must DELETE them
-- BEFORE this migration runs — see the accompanying release notes.
ALTER TABLE auth_tokens
  ADD CONSTRAINT auth_tokens_identifier_required
  CHECK (token IS NOT NULL OR hashed_token IS NOT NULL);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE auth_tokens DROP CONSTRAINT IF EXISTS auth_tokens_identifier_required;
-- +goose StatementEnd
