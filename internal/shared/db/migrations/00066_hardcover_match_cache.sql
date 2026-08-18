-- +goose Up
-- +goose StatementBegin

-- GLOBAL, cross-user Hardcover match cache (gaka-wzgr). A resolved match
-- (ASIN or ISBN-13 → hardcover_book_id/edition_id) is an OBJECTIVE fact about a
-- BOOK, not about the user who happened to trigger the sweep. Once ANY user
-- resolves it via the exact-id ladder we cache it here so every future user (and
-- every future re-import) resolves the same identity with ZERO Hardcover API
-- calls. Only exact-id hits (asin / isbn13) land here — the fuzzy Typesense
-- search rung is deliberately NEVER cached, since a wrong edition would then
-- poison the match for every user.
CREATE TABLE IF NOT EXISTS hardcover_match_cache (
    id_type              text NOT NULL CHECK (id_type IN ('asin','isbn13')),
    external_id          text NOT NULL,
    hardcover_book_id    bigint NOT NULL,
    hardcover_edition_id bigint,
    method               text NOT NULL,
    matched_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id_type, external_id)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS hardcover_match_cache;
-- +goose StatementEnd
