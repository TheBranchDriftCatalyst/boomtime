-- +goose Up
-- +goose StatementBegin

-- gaka-dfd: add an "enabled" flag to curation_rules so users can pause a
-- rename or hide rule without deleting it. A disabled rule keeps its
-- definition but stops applying at query time (LoadRenameSets and
-- LoadHiddenSets filter WHERE enabled = true). Reversible with one click
-- via POST /api/v1/users/current/curation/:id/toggle.
--
-- Default true so every existing rule stays active after the migration —
-- the semantic change only shows up when a user explicitly pauses a rule.
ALTER TABLE public.curation_rules
    ADD COLUMN enabled boolean NOT NULL DEFAULT true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.curation_rules DROP COLUMN IF EXISTS enabled;
-- +goose StatementEnd
