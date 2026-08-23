-- +goose Up
-- +goose StatementBegin

-- User-model substrate (boom-0oe.1 / boom-93f). Additive + default-safe:
--   role         TEXT        NOT NULL DEFAULT 'full'
--   capabilities JSONB       NOT NULL DEFAULT '{}'::jsonb
--   disabled_at  TIMESTAMPTZ NULL
--
-- EXISTING rows land at role='full', capabilities='{}', disabled_at=NULL —
-- the identity element for the new gating logic (see internal/auth). No code
-- path reads these columns until BOOM_FEATURE_USER_MODEL flips on, so every
-- existing test + prod behavior stays byte-for-byte identical with the flag
-- off (the default).
--
-- role values are validated in Go (auth.ValidRole): 'full' | 'light' |
-- 'service' | 'admin'. TEXT (not a PG enum) so adding a tier is a one-line Go
-- change (matches the curation_rules.action pattern).
--
-- capabilities is a per-user override blob; '{}' means "use the role's
-- defaults" (auth.BuildIdentity). disabled_at NULL = active; when set, the
-- Identity resolver fails closed (see apihelpers.Identify).
ALTER TABLE public.users
    ADD COLUMN role         TEXT        NOT NULL DEFAULT 'full',
    ADD COLUMN capabilities JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN disabled_at  TIMESTAMPTZ NULL;

-- Partial index for "list disabled" / audit + lets the resolver skip a full
-- scan when the column is sparse.
CREATE INDEX users_disabled_at_idx ON public.users (disabled_at)
    WHERE disabled_at IS NOT NULL;

-- External-identity linkage for OIDC (Authentik, then federated GitHub/etc).
-- One row per (provider, sub). NULL for local-password-only users. A user can
-- hold BOTH a local password AND an OIDC link during migration. Deleted with
-- the user (ON DELETE CASCADE).
--
-- `claims` caches the last-seen NON-sensitive claim payload (email,
-- preferred_username, groups) for audit + admin UI. It NEVER holds an
-- id_token / access_token / refresh_token — those live in the session tables
-- with the hashed-at-rest treatment.
CREATE TABLE public.user_external_identities (
    id           UUID        PRIMARY KEY DEFAULT public.uuid_generate_v4(),
    username     TEXT        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    provider     TEXT        NOT NULL,
    sub          TEXT        NOT NULL,
    email        TEXT,
    claims       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, sub)
);

CREATE INDEX user_external_identities_username_idx
    ON public.user_external_identities (username);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_external_identities;
DROP INDEX IF EXISTS public.users_disabled_at_idx;
ALTER TABLE public.users
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS disabled_at;
-- +goose StatementEnd
