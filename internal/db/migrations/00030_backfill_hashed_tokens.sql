-- +goose Up
-- +goose StatementBegin

-- Phase 1 of the raw-column cutover (gaka-awh.5 / gaka-uj7).
-- Populates every legacy raw-token row's hashed_* companion via SHA-256, so
-- v31 can safely DROP the raw `token` / `refresh_token` columns.
--
-- pgcrypto ships with contrib and is installable by the CNPG-managed
-- postgres user without extra superuser plumbing.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- auth_tokens: hash raw → hashed_token where hashed_token is missing.
-- WHERE guard makes this fully idempotent — reruns are no-ops.
UPDATE auth_tokens
SET    hashed_token = digest(token, 'sha256')
WHERE  token IS NOT NULL
  AND  hashed_token IS NULL;

-- refresh_tokens: same treatment on the refresh column.
UPDATE refresh_tokens
SET    hashed_refresh_token = digest(refresh_token, 'sha256')
WHERE  refresh_token IS NOT NULL
  AND  hashed_refresh_token IS NULL;

-- Post-migration invariant: no row anywhere has raw-only.
-- If either check fails the migration aborts and v31 can't proceed.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM auth_tokens WHERE token IS NOT NULL AND hashed_token IS NULL) THEN
    RAISE EXCEPTION 'auth_tokens still has raw-only rows after v30 backfill — v31 must not run';
  END IF;
  IF EXISTS (SELECT 1 FROM refresh_tokens WHERE refresh_token IS NOT NULL AND hashed_refresh_token IS NULL) THEN
    RAISE EXCEPTION 'refresh_tokens still has raw-only rows after v30 backfill — v31 must not run';
  END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- No-op down: we cannot un-hash a token. The raw column is still around
-- (v31 drops it). Rolling back v30 just leaves both columns populated,
-- which the dual-path code paths already handle.
-- +goose StatementEnd
