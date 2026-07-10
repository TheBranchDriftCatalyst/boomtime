-- +goose Up
-- Coarse daily rollup for the Overview stats (get_user_activity) at the default
-- 15-min limit. Dimensions the payload actually breaks down by (project/language/
-- editor/platform/machine) — no entity/branch, so cardinality stays low. Made a
-- trivial SUM by the precomputed gap_seconds; maintained incrementally on ingest.
CREATE TABLE IF NOT EXISTS hb_rollup_daily (
    sender text NOT NULL,
    day date NOT NULL,
    project text NOT NULL,
    language text NOT NULL,
    editor text NOT NULL,
    platform text NOT NULL,
    machine text NOT NULL,
    total_seconds bigint NOT NULL,
    PRIMARY KEY (sender, day, project, language, editor, platform, machine)
);
CREATE INDEX IF NOT EXISTS hb_rollup_daily_sender_day_idx ON hb_rollup_daily (sender, day);

-- One-time backfill from existing heartbeats (no re-import needed).
INSERT INTO hb_rollup_daily (sender, day, project, language, editor, platform, machine, total_seconds)
SELECT
    sender,
    time_sent::date AS day,
    coalesce(project, 'Other'),
    coalesce(language, 'Other'),
    coalesce(editor, 'Other'),
    coalesce(platform, 'Other'),
    coalesce(machine, 'Other'),
    sum(CASE WHEN gap_seconds <= 900 THEN gap_seconds ELSE 0 END)
FROM heartbeats
GROUP BY sender, time_sent::date, coalesce(project, 'Other'), coalesce(language, 'Other'),
    coalesce(editor, 'Other'), coalesce(platform, 'Other'), coalesce(machine, 'Other')
ON CONFLICT DO NOTHING;
-- +goose Down
DROP TABLE IF EXISTS hb_rollup_daily;
