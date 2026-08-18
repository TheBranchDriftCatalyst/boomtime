-- +goose Up
-- +goose StatementBegin

-- gaka social-card: per-user customization for the public-profile OpenGraph
-- social card (the /api/public/profile/:slug/og.png image + the og:* meta
-- injected on /p/:slug). Two tiny, non-sensitive knobs stored right next to
-- the existing public_profile_enabled / public_slug columns on users:
--
--   public_card_theme    — widget SVG theme name ("dark" synthwave default,
--                          or "light"). Empty string = fall back to the
--                          renderer default (dark). NOT NULL DEFAULT '' so
--                          every existing user gets the default card.
--   public_card_tagline  — an optional short line the owner can set to feed
--                          the card headline / og:description. Empty = the
--                          endpoint auto-builds a headline from public stats.
--
-- Both are PUBLIC by nature (they only ever surface on the already-public
-- profile), so no encryption / scrub concerns — unlike encrypted_wakatime_key.
ALTER TABLE public.users
    ADD COLUMN public_card_theme   text NOT NULL DEFAULT '',
    ADD COLUMN public_card_tagline text NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users DROP COLUMN IF EXISTS public_card_theme;
ALTER TABLE public.users DROP COLUMN IF EXISTS public_card_tagline;
-- +goose StatementEnd
