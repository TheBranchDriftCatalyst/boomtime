-- +goose Up
-- +goose StatementBegin

-- Local mirror of the user's FULL Hardcover shelf (catalyst-books shelf-match) —
-- the candidate pool the match sweep scores unmatched reading_items against. The
-- exact-id/fuzzy ladder (internal/hardcover/match.go) misses books the user HAS
-- shelved on Hardcover but that don't share an ASIN/ISBN with our Kindle/Audible
-- edition (~93% of Kindle rows fall to the fuzzy tail + mostly miss). The pull
-- already fetches the shelf; persisting it here turns "match against the user's
-- own curated shelf" into a LOCAL, zero-API, high-precision rung.
--
-- Distinct from the two existing match stores:
--   - reading_items.hardcover_* — the resolved link, PER USER per row.
--   - hardcover_match_cache     — the GLOBAL exact-id (asin/isbn13) resolution.
-- This table is the user's shelf itself (ALL statuses: want+reading+read), the
-- INPUT to matching, not an output. SILOED like the other reading tables: it
-- cascade-deletes with the user and never writes into heartbeats/stats.
--
--   owner             — the user (FK to users.username, cascade)
--   hardcover_book_id — the Hardcover book id (the shelf entry's identity)
--   title / author    — display + the tokens the local scorer matches on (author
--                       is the first contribution's name; "" when Hardcover has none)
--   slug              — the /books/<slug> deep-link segment carried onto a match
--   status            — the shelf status string (want|reading|read|paused|dnf|"")
--   updated_at        — Hardcover's own updated_at for the shelf entry (nullable)
--   synced_at         — when this pull last refreshed the row
CREATE TABLE IF NOT EXISTS public.hardcover_user_shelf (
    owner             text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    hardcover_book_id bigint      NOT NULL,
    title             text        NOT NULL DEFAULT '',
    author            text        NOT NULL DEFAULT '',
    slug              text        NOT NULL DEFAULT '',
    status            text        NOT NULL DEFAULT '',
    updated_at        timestamptz,
    synced_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner, hardcover_book_id)
);

-- The shelf pass loads a user's whole shelf once per sweep (ListHardcoverShelf);
-- the PK's leading `owner` already serves that (owner, *) scan, so no extra index.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.hardcover_user_shelf;
-- +goose StatementEnd
