-- +goose Up
-- +goose StatementBegin

-- Phase 2 of the raw-column cutover (gaka-uj7). v30's SQL backfill guaranteed
-- every row now has a populated hashed_* companion + asserted zero raw-only
-- rows before completing. Safe to drop the raw columns entirely.
--
-- Also drops the v27-era partial UNIQUE index that referenced the raw column,
-- and drops the CHECK constraint from v29 (now trivially satisfied because
-- hashed_token becomes the only identifier).

DROP INDEX IF EXISTS auth_tokens_token_key;
DROP INDEX IF EXISTS refresh_tokens_refresh_token_key;

ALTER TABLE auth_tokens
  DROP CONSTRAINT IF EXISTS auth_tokens_identifier_required;

ALTER TABLE auth_tokens    DROP COLUMN IF EXISTS token;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS refresh_token;

-- Post-DROP: hashed_token is now the sole identifier. Add NOT NULL so any
-- future INSERT that forgets to hash blows up loudly instead of creating
-- another ghost row.
ALTER TABLE auth_tokens    ALTER COLUMN hashed_token         SET NOT NULL;
ALTER TABLE refresh_tokens ALTER COLUMN hashed_refresh_token SET NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- No down: raw column data cannot be recovered from the hash. Rolling back
-- past v31 requires restoring from a pre-v31 backup.
-- +goose StatementEnd
