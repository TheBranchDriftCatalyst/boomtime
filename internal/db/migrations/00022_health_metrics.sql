-- +goose Up

-- Overlay Apple Watch / HealthKit metrics onto the WakaTime domain.
--
-- Two flavors of data:
--   1. Workouts — discrete sessions with an authoritative duration Apple hands
--      us. These fit the heartbeat abstraction (one event, one time, one
--      "sender"), so they land in `heartbeats` with ty='workout' and a synthetic
--      entity ('workout:<uuid>'). The five workout_* columns below give the
--      aggregation layer everything it needs to treat a workout as first-class
--      time-spent alongside a project.
--   2. Raw samples — HR readings, step counts, active-energy deltas, HRV,
--      sleep stages, mindful minutes. These do NOT have "time spent" semantics
--      (a single HR reading isn't a session), so they get their own tables:
--      health_samples for the raw stream, health_rollup_daily for the fast
--      path. Aggregation is min/avg/max/sum, not sum-of-gap-seconds.
--
-- The rollup query in internal/db/ingest.go's refreshRollup() is updated in
-- the same PR to `COALESCE(workout_duration_s, gap-bounded)`, so any existing
-- surface that already reads hb_rollup_daily (Overview totals, punchcard,
-- momentum, projects list) picks up workouts as time-spent for free.

ALTER TABLE heartbeats
    ADD COLUMN workout_kind        TEXT,
    ADD COLUMN workout_duration_s  INTEGER,
    ADD COLUMN workout_kcal        REAL,
    ADD COLUMN workout_avg_hr      INTEGER,
    ADD COLUMN workout_distance_m  REAL;

-- Per-workout deep detail. Route + HR series can be tens of KB each, so we
-- store them out of line as JSONB rather than widening heartbeats.
-- ON DELETE CASCADE keeps this table consistent if a heartbeat row is ever
-- deleted (curation, redaction) — details are meaningless without the parent.
CREATE TABLE workout_details (
    heartbeat_id BIGINT PRIMARY KEY REFERENCES heartbeats(id) ON DELETE CASCADE,
    source_uuid  TEXT NOT NULL,
    hr_series    JSONB,
    route        JSONB
);
CREATE UNIQUE INDEX idx_workout_details_source_uuid ON workout_details(source_uuid);

CREATE TABLE health_samples (
    id           BIGSERIAL PRIMARY KEY,
    owner        TEXT NOT NULL REFERENCES users(username),
    kind         TEXT NOT NULL,
    unit         TEXT NOT NULL,
    qty          REAL,
    q_min        REAL,
    q_avg        REAL,
    q_max        REAL,
    ts_start     TIMESTAMPTZ NOT NULL,
    ts_end       TIMESTAMPTZ,
    meta         JSONB,
    workout_id   BIGINT REFERENCES heartbeats(id) ON DELETE CASCADE
);

-- Dedupe: HealthKit's anchor cursor should prevent doubles, but a client that
-- loses its anchor and re-syncs would otherwise inflate totals.
CREATE UNIQUE INDEX idx_health_samples_dedupe
    ON health_samples(owner, kind, ts_start, COALESCE(ts_end, ts_start));
CREATE INDEX idx_health_samples_owner_kind_ts ON health_samples(owner, kind, ts_start);
CREATE INDEX idx_health_samples_workout ON health_samples(workout_id) WHERE workout_id IS NOT NULL;

-- Daily rollup for fast Wellness card + widget queries. Mirrors hb_rollup_daily's
-- role: aggregations are DELETE+INSERT-refreshed on ingest for the affected days,
-- inside the same transaction as the raw insert (see internal/db/health.go).
CREATE TABLE health_rollup_daily (
    owner         TEXT NOT NULL,
    day           DATE NOT NULL,
    kind          TEXT NOT NULL,
    total_qty     REAL,
    avg_qty       REAL,
    min_qty       REAL,
    max_qty       REAL,
    sample_count  INTEGER NOT NULL,
    PRIMARY KEY (owner, day, kind)
);

-- +goose Down
DROP TABLE health_rollup_daily;
DROP TABLE health_samples;
DROP TABLE workout_details;
ALTER TABLE heartbeats
    DROP COLUMN workout_kind,
    DROP COLUMN workout_duration_s,
    DROP COLUMN workout_kcal,
    DROP COLUMN workout_avg_hr,
    DROP COLUMN workout_distance_m;
