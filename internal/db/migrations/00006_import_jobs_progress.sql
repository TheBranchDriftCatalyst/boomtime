-- +goose Up

-- Extend import_jobs into a first-class, durable, resumable job record.
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS owner TEXT;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS start_date TIMESTAMPTZ;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS end_date TIMESTAMPTZ;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS total_days INT;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS processed_days INT NOT NULL DEFAULT 0;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS imported_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS current_day DATE;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE import_jobs ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;

-- New state vocabulary: 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'.
-- Migrate the previous default ('enqueued') to the new 'queued'.
UPDATE import_jobs SET state = 'queued' WHERE state = 'enqueued';
ALTER TABLE import_jobs ALTER COLUMN state SET DEFAULT 'queued';

CREATE INDEX IF NOT EXISTS import_jobs_owner_idx ON import_jobs (owner);

CREATE TABLE IF NOT EXISTS import_job_logs (
    id BIGSERIAL PRIMARY KEY,
    job_id INT NOT NULL REFERENCES import_jobs (id) ON DELETE CASCADE,
    ts TIMESTAMPTZ NOT NULL DEFAULT now(),
    level TEXT NOT NULL,
    message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS import_job_logs_job_id_id_idx ON import_job_logs (job_id, id);

-- +goose Down
DROP TABLE IF EXISTS import_job_logs;
ALTER TABLE import_jobs ALTER COLUMN state SET DEFAULT 'enqueued';
ALTER TABLE import_jobs DROP COLUMN IF EXISTS finished_at;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS started_at;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS current_day;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS imported_count;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS processed_days;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS total_days;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS end_date;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS start_date;
ALTER TABLE import_jobs DROP COLUMN IF EXISTS owner;
