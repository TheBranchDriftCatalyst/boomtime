-- +goose Up
-- +goose StatementBegin

-- Rolling per-owner window of observed advance INTERVALS (catalyst-books §5.1) —
-- the queryable backing for the reading-monitor's interval RECOMMENDATION. The
-- engine emits advance_interval_seconds to Prometheus, but that histogram is not
-- queryable from the admin endpoint; so on each detected INTRA-session advance
-- (the same point it observes the histogram) the engine also appends one row
-- here, and the GET derives p50/p90 over the recent window.
--
-- SILOED like the other reading-monitor tables: ON DELETE CASCADE with the user,
-- never writes into heartbeats/stats. Rows are cheap (one per intra-session
-- advance) and the recommendation only reads a recent window (RecommendLookback),
-- so growth is slow + bounded at read time.
--
--   owner         — the user
--   source        — the reading source ('kindle')
--   interval_secs — wall-clock seconds since the same book's previous advance
--                   (Amazon creationTime deltas — the intra-session cadence)
--   dloc          — the location delta of this advance (opaque Kindle units)
--   at            — the advance's event time (Amazon creationTime)
CREATE TABLE IF NOT EXISTS public.kindle_reading_monitor_advances (
    id            bigserial        PRIMARY KEY,
    owner         text             NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source        text             NOT NULL DEFAULT 'kindle',
    interval_secs double precision NOT NULL,
    dloc          bigint           NOT NULL,
    at            timestamptz      NOT NULL DEFAULT now()
);

-- The recommendation reads a user's recent samples; this index serves the
-- (owner, at >= since) window scan + the newest-first cap.
CREATE INDEX IF NOT EXISTS kindle_reading_monitor_advances_owner_at_idx
    ON public.kindle_reading_monitor_advances (owner, at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.kindle_reading_monitor_advances;
-- +goose StatementEnd
