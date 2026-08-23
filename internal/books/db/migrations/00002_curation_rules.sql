-- +goose Up
-- +goose StatementBegin

-- curation_rules (boom-omt2): the cross-domain query DSL's applyCanonicalPins
-- (db.LoadPinnedSet: SELECT match_value ... WHERE sender/action/axis + enabled)
-- reads this per owner/axis. The standalone mounts the query DSL (POST
-- /api/v1/query) for the books library view, so without this table every library
-- query 500s ("relation curation_rules does not exist"). Books curation also
-- persists here. Empty by default -> canonical pins are a no-op until the owner
-- adds rules. Mirrors the host's curation_rules shape (the columns LoadPinnedSet
-- + books curation touch); FK'd to the single-owner users stub.
CREATE TABLE IF NOT EXISTS public.curation_rules (
    id          bigserial   PRIMARY KEY,
    sender      text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    axis        text        NOT NULL,
    action      text        NOT NULL,
    match_value text        NOT NULL,
    new_value   text,
    match_type  text        NOT NULL DEFAULT 'exact',
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_curation_rules_sender_axis_action
    ON public.curation_rules (sender, axis, action) WHERE enabled;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.curation_rules;
-- +goose StatementEnd
