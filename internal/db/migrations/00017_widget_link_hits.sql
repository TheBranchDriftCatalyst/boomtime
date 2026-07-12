-- +goose Up

-- gaka-hsj follow-up: track hits per widget link so the Settings UI can show
-- 'last requested N minutes ago' and a click-through popover of the unique
-- origin set (Referer values). last_used_at is a cheap top-line stat;
-- origins is a bounded JSON array of {origin, count, lastSeen} tuples,
-- capped in application code at 20 most-recent entries.
ALTER TABLE widget_links
    ADD COLUMN last_used_at timestamptz,
    ADD COLUMN origins jsonb NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE widget_links
    DROP COLUMN last_used_at,
    DROP COLUMN origins;
