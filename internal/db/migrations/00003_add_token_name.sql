-- +goose Up
ALTER TABLE auth_tokens ADD token_name TEXT;
ALTER TABLE auth_tokens ADD token_description TEXT;
-- +goose Down
ALTER TABLE auth_tokens DROP COLUMN IF EXISTS token_name;
ALTER TABLE auth_tokens DROP COLUMN IF EXISTS token_description;
