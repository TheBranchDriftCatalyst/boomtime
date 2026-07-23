-- +goose Up

-- gaka-b5x.2 (part 2 / migration 00026): the raw refresh_token / token
-- columns were originally PRIMARY KEY (implicit NOT NULL). New session-mint
-- writes leave them NULL (only hashed_* is populated), so we must drop the
-- primary key + NOT NULL constraint here.
--
-- Uniqueness is preserved by two partial UNIQUE indexes per table:
--   * <raw>_key  WHERE <raw> IS NOT NULL   → legacy rows keep uniqueness
--   * <hashed>_key WHERE <hashed> IS NOT NULL → new rows get uniqueness
-- Partial indexes are what allow the NULL half of each column to coexist
-- (a full UNIQUE would fold multiple NULLs into one another only under
-- Postgres 15+, but partial is the portable & explicit story).
--
-- Split out from 00026 so a repo that already applied 00026 (which just
-- added the columns) picks up the constraint relaxation as a clean second
-- step. Idempotent: DROP CONSTRAINT IF EXISTS handles the never-applied
-- + already-applied cases equally.

ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_pkey;
ALTER TABLE refresh_tokens ALTER COLUMN refresh_token DROP NOT NULL;

ALTER TABLE auth_tokens DROP CONSTRAINT IF EXISTS auth_tokens_pkey;
ALTER TABLE auth_tokens ALTER COLUMN token DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS refresh_tokens_refresh_token_key
    ON refresh_tokens (refresh_token)
    WHERE refresh_token IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS auth_tokens_token_key
    ON auth_tokens (token)
    WHERE token IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS refresh_tokens_hashed_refresh_token_key
    ON refresh_tokens (hashed_refresh_token)
    WHERE hashed_refresh_token IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS auth_tokens_hashed_token_key
    ON auth_tokens (hashed_token)
    WHERE hashed_token IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS auth_tokens_hashed_token_key;
DROP INDEX IF EXISTS refresh_tokens_hashed_refresh_token_key;
DROP INDEX IF EXISTS auth_tokens_token_key;
DROP INDEX IF EXISTS refresh_tokens_refresh_token_key;

-- Best-effort restore of PK + NOT NULL: works only if no NULL rows exist
-- (immediately after Down, before new session-mint traffic hits).
ALTER TABLE auth_tokens ALTER COLUMN token SET NOT NULL;
ALTER TABLE refresh_tokens ALTER COLUMN refresh_token SET NOT NULL;
ALTER TABLE auth_tokens ADD PRIMARY KEY (token);
ALTER TABLE refresh_tokens ADD PRIMARY KEY (refresh_token);
