-- +goose Up
-- +goose StatementBegin

-- OIDC browser sessions (boom-0oe.11). When BOOM_AUTH_PROVIDER=oidc the web
-- session is NOT a boomtime refresh_token — it's an opaque cookie mapping to
-- this server-side record holding the id_token expiry (session validity) and
-- the provider refresh_token (hashed at rest, same posture as auth_tokens).
-- Separate from refresh_tokens (design §9.1): the shapes differ enough
-- (id_token_hint for RP-initiated logout, provider refresh) that co-mingling
-- risks confusion.
--
-- hashed_session_id = SHA-256 of the opaque cookie value (never store the raw
-- cookie). Deleted with the user (ON DELETE CASCADE).
CREATE TABLE public.oidc_sessions (
    hashed_session_id bytea       PRIMARY KEY,
    username          text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    id_token_expiry   timestamptz NOT NULL,
    hashed_refresh    bytea,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX oidc_sessions_username_idx ON public.oidc_sessions (username);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.oidc_sessions;
-- +goose StatementEnd
