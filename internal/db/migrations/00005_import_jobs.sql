-- +goose Up
CREATE TABLE IF NOT EXISTS import_jobs (
    id SERIAL PRIMARY KEY,
    value JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'enqueued',
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS import_jobs_state_idx ON import_jobs (state);
-- +goose Down
DROP TABLE IF EXISTS import_jobs;
