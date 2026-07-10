-- +goose Up
CREATE TABLE IF NOT EXISTS spaces (
    id         SERIAL PRIMARY KEY,
    owner      TEXT NOT NULL,
    name       TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, name)
);

CREATE TABLE IF NOT EXISTS space_rules (
    id          SERIAL PRIMARY KEY,
    space_id    INT NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    axis        TEXT NOT NULL,
    match_value TEXT NOT NULL,
    match_type  TEXT NOT NULL DEFAULT 'exact'
);

CREATE INDEX IF NOT EXISTS space_rules_space_id_idx ON space_rules (space_id);

-- +goose Down
DROP TABLE IF EXISTS space_rules;
DROP TABLE IF EXISTS spaces;
