-- +goose Up

-- gaka-unq.1: persist wakatime.com API schema-drift findings per import job so
-- the Import page can show a WARNING banner on historical runs. Nullable JSONB
-- keeps the round-trip simple (Go json.RawMessage <-> FE array/null).
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS drift JSONB;

-- +goose Down
ALTER TABLE import_jobs DROP COLUMN IF EXISTS drift;
