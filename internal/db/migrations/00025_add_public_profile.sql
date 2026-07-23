-- +goose Up

-- gaka-6jm.1: opt-in public read-only profile.
--
-- A user who flips the switch picks a URL-safe slug that becomes their
-- public profile handle at /p/<slug>. The public payload is served by
-- internal/handler/profile.go and is ALWAYS routed through
-- internal/widget.Scrub so hidden values (project/language/editor/etc.)
-- never leak into the public JSON — same public-safe contract as the
-- widget SVG endpoint.
--
-- public_profile_enabled: NOT NULL DEFAULT false so existing rows land in
-- the safe (off) state. Users must explicitly enable to publish.
--
-- public_slug: nullable — a user can save the intent to enable while
-- still deciding on a slug, and DELETE-toggling off shouldn't force us to
-- drop the slug (they may want to re-enable with the same slug later).
-- The partial UNIQUE index below only enforces uniqueness on NON-NULL
-- slugs, so multiple users can coexist with NULL slugs.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS public_profile_enabled BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS public_slug TEXT NULL;

-- Partial unique: NULL slugs don't collide; a slug is unique when set.
CREATE UNIQUE INDEX IF NOT EXISTS users_public_slug_key
    ON users (public_slug)
    WHERE public_slug IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS users_public_slug_key;

ALTER TABLE users
    DROP COLUMN IF EXISTS public_slug;

ALTER TABLE users
    DROP COLUMN IF EXISTS public_profile_enabled;
