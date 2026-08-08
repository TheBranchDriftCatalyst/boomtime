-- +goose Up
-- +goose StatementBegin

-- gaka-scrub: an "apply at ingest" flag on rename rules. A curation rename rule
-- already applies at query-time (remapExpr, reversible view) and on-demand
-- destructively (ApplyRename). This flag adds a FOURTH application point: the
-- ingest path (internal/ingest.storeAndRespond) rewrites newly-stored heartbeat
-- fields via db.LoadIngestRenameRules + the Go applier — irreversible for new
-- rows, the "scrubber" behavior (e.g. strip a '/Users/x/' entity prefix).
--
-- Default false so every existing rename rule keeps today's query-time-only
-- semantics. apply_at_ingest rules are EXCLUDED from LoadRenameSets (query-time)
-- so an ingest-baked row is never re-transformed at read.
ALTER TABLE public.curation_rules
    ADD COLUMN apply_at_ingest boolean NOT NULL DEFAULT false;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.curation_rules DROP COLUMN IF EXISTS apply_at_ingest;
-- +goose StatementEnd
