-- +goose Up
-- Widen hb_rollup_daily from 5 axes (project/language/editor/platform/machine)
-- to 8 (+category, +plugin, +branch) so hides and Space-scope rules on those
-- axes can be applied as spliced predicates on the rollup instead of forcing
-- the raw heartbeats scan. Output grain of get_user_activity_rollup is
-- unchanged — the new columns exist for FILTERING only, then get collapsed
-- back to the same 5-axis grain by a CTE GROUP BY. Entity stays OUT (per-file,
-- near raw cardinality).
--
-- The PK must change and the rollup is fully derived data, so drop + recreate
-- + full backfill (same pattern as 00009) is simpler and safer than ALTER + PK
-- surgery.
DROP TABLE IF EXISTS hb_rollup_daily;
CREATE TABLE hb_rollup_daily (
    sender text NOT NULL,
    day date NOT NULL,
    project text NOT NULL,
    language text NOT NULL,
    editor text NOT NULL,
    platform text NOT NULL,
    machine text NOT NULL,
    category text NOT NULL,
    plugin text NOT NULL,
    branch text NOT NULL,
    total_seconds bigint NOT NULL,
    PRIMARY KEY (sender, day, project, language, editor, platform, machine, category, plugin, branch)
);
CREATE INDEX hb_rollup_daily_sender_day_idx ON hb_rollup_daily (sender, day);

-- One-time backfill from existing heartbeats (no re-import needed). Byte-parity
-- with RefreshRollup's INSERT SELECT so ResyncDerived produces identical rows.
INSERT INTO hb_rollup_daily (sender, day, project, language, editor, platform, machine, category, plugin, branch, total_seconds)
SELECT
    sender,
    time_sent::date AS day,
    coalesce(project, 'Other'),
    coalesce(language, 'Other'),
    coalesce(editor, 'Other'),
    coalesce(platform, 'Other'),
    coalesce(machine, 'Other'),
    coalesce(category, 'Other'),
    coalesce(plugin, 'Other'),
    coalesce(branch, 'Other'),
    sum(CASE WHEN gap_seconds <= 900 THEN gap_seconds ELSE 0 END)
FROM heartbeats
GROUP BY sender, time_sent::date, coalesce(project, 'Other'), coalesce(language, 'Other'),
    coalesce(editor, 'Other'), coalesce(platform, 'Other'), coalesce(machine, 'Other'),
    coalesce(category, 'Other'), coalesce(plugin, 'Other'), coalesce(branch, 'Other');

-- +goose Down
-- Restore the 5-axis shape (same as 00009's Up).
DROP TABLE IF EXISTS hb_rollup_daily;
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
