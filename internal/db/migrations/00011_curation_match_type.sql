-- +goose Up
-- Add an optional match mode to rename/hide rules: 'exact' (default, backward
-- compatible) matches a literal value; 'regex' matches every value where the raw
-- column matches the Postgres regex (~). Existing rules become 'exact'.
ALTER TABLE curation_rules ADD COLUMN IF NOT EXISTS match_type TEXT NOT NULL DEFAULT 'exact';

-- The (sender,axis,action,match_value) unique index still applies; a regex rule's
-- match_value holds the pattern, so exact 'Meeting' and regex 'Meeting' can't
-- collide within the same axis/action — extend uniqueness to include match_type.
DROP INDEX IF EXISTS curation_rules_unique_idx;
CREATE UNIQUE INDEX IF NOT EXISTS curation_rules_unique_idx
    ON curation_rules (sender, axis, action, match_type, match_value);

-- +goose Down
DROP INDEX IF EXISTS curation_rules_unique_idx;
CREATE UNIQUE INDEX IF NOT EXISTS curation_rules_unique_idx
    ON curation_rules (sender, axis, action, match_value);
ALTER TABLE curation_rules DROP COLUMN IF EXISTS match_type;
