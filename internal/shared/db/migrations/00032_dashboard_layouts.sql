-- +goose Up
-- +goose StatementBegin

-- dashboard_layouts (boom-keb): persisted per-user, per-scope layout JSON
-- for the composable dashboard grid. v1 scope = "public_profile" only; the
-- schema is deliberately future-proofed with a `scope` column so the same
-- table can back the authed Overview and per-page dashboards later without
-- another migration.
--
-- Payload shape (documented, not enforced by DB — the handler validates):
--   {
--     "cols": 12,
--     "widgets": [
--       {"i": "stats-card-with-grade", "x": 0, "y": 0, "w": 6, "h": 3, "view": null},
--       {"i": "top-langs",             "x": 6, "y": 0, "w": 6, "h": 3, "view": "bar"}
--     ]
--   }
--
-- The `i` values are widget catalog KIND ids (see internal/widget/render.go's
-- `kinds` map / web/shared/features/widgets/catalog.ts). One catalog, one
-- scrubber, one renderer contract — the layout is just a placement of those
-- kinds.

-- Use uuid_generate_v4() from uuid-ossp (installed in the baseline).
-- pgcrypto's gen_random_uuid isn't always in scope in older dev DBs;
-- uuid-ossp is what widget_defs / badges / widget_links already use.
CREATE TABLE public.dashboard_layouts (
    id UUID PRIMARY KEY DEFAULT public.uuid_generate_v4(),
    owner TEXT NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    layout JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, scope)
);

CREATE INDEX dashboard_layouts_owner ON public.dashboard_layouts(owner);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.dashboard_layouts;
-- +goose StatementEnd
