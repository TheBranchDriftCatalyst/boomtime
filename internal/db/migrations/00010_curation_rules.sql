-- +goose Up
CREATE TABLE IF NOT EXISTS curation_rules (
    id          SERIAL PRIMARY KEY,
    sender      TEXT NOT NULL,
    axis        TEXT NOT NULL,
    action      TEXT NOT NULL,
    match_value TEXT NOT NULL,
    new_value   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Prevent duplicate rules for the same (sender, axis, action, match_value).
CREATE UNIQUE INDEX IF NOT EXISTS curation_rules_unique_idx
    ON curation_rules (sender, axis, action, match_value);

CREATE INDEX IF NOT EXISTS curation_rules_sender_action_idx
    ON curation_rules (sender, action);

-- +goose Down
DROP TABLE IF EXISTS curation_rules;
