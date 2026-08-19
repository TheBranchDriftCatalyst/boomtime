-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- catalyst-books STANDALONE baseline (gaka-zp2s books-standalone).
--
-- Consolidated FINAL shape of every table internal/books reads or writes,
-- distilled from the host's incremental migrations 00057–00080. This DB starts
-- EMPTY, so one forward migration reflecting the end state is equivalent to
-- replaying the whole history.
--
-- Base case = ONE real user (gaka-zp2s books-scope). The standalone runs as a
-- single local owner, and the owner<->books relation is KEPT exactly as the
-- host has it: every books DATA table's `owner` column carries the host's
--   owner text NOT NULL REFERENCES public.users(username) ON DELETE CASCADE
-- FK — matching the host schema table-for-table (the two host tables that are
-- deliberately FK-free, reading_events + notifications, stay FK-free here too).
-- Indexes and UNIQUE / PRIMARY KEY constraints are preserved verbatim.
--
-- The FK target is a MINIMAL `users` stub (below): the ONE real users row.
-- cmd/catalyst-books INSERTs the single owner into it right after these
-- migrations run and BEFORE it serves any request, so the row every books
-- table FKs to exists before any owner-scoped write. The books DAL
-- (internal/shared/db/{amazon_device,hardcover_token,reading_monitor}.go) also
-- stores the owner's connect CREDENTIALS + reading-monitor settings as COLUMNS
-- ON `users`, so the stub holds the owner key + exactly those columns; NO
-- password, salt, role, capabilities, wakatime, OIDC, timezone, avatar, or
-- public-profile columns.
-- ============================================================================

-- ---- minimal owner-credential stub -----------------------------------------
-- Only the columns the standalone-mounted books handlers touch:
--   * Amazon device credential  (amazon_device.go — GetAmazonConnection, save)
--   * Hardcover bearer token     (hardcover_token.go — Get/Connect/Disconnect)
--   * reading-monitor settings   (reading_monitor.go — ReadingMonitorStatus)
CREATE TABLE IF NOT EXISTS public.users (
    username                          text PRIMARY KEY,
    encrypted_amazon_device           bytea,
    amazon_device_status              text,
    amazon_device_checked_at          timestamptz,
    encrypted_hardcover_key           bytea,
    hardcover_key_status              text,
    hardcover_key_checked_at          timestamptz,
    reading_monitor_enabled           boolean     NOT NULL DEFAULT false,
    reading_monitor_mode              text        NOT NULL DEFAULT 'debounced',
    reading_monitor_calibrating_until timestamptz
);

-- ---- reading_items (final shape: 00058+00060+00063+00065+00069+00070+00071+00077+00080)
CREATE TABLE IF NOT EXISTS public.reading_items (
    id                          bigserial   PRIMARY KEY,
    owner                       text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source                      text        NOT NULL,
    external_id                 text        NOT NULL,
    title                       text        NOT NULL DEFAULT '',
    authors                     text        NOT NULL DEFAULT '',
    cover_url                   text        NOT NULL DEFAULT '',
    status                      text        NOT NULL DEFAULT '',
    progress_percent            integer     NOT NULL DEFAULT 0,
    finished                    boolean     NOT NULL DEFAULT false,
    started_at                  timestamptz,
    finished_at                 timestamptz,
    rating                      numeric,
    raw_meta                    jsonb,
    synced_at                   timestamptz NOT NULL DEFAULT now(),
    -- 00060 metadata
    subtitle                    text        NOT NULL DEFAULT '',
    narrators                   text        NOT NULL DEFAULT '',
    series                      text        NOT NULL DEFAULT '',
    runtime_min                 integer,
    purchase_date               timestamptz,
    isbn                        text        NOT NULL DEFAULT '',
    amazon_asin                 text        NOT NULL DEFAULT '',
    genres                      jsonb,
    goodreads_rating            numeric,
    -- 00063 hardcover link
    hardcover_book_id           bigint,
    hardcover_edition_id        bigint,
    hardcover_status            text,
    hardcover_match_confidence  text,
    hardcover_matched_at        timestamptz,
    hardcover_pushed_at         timestamptz,
    hardcover_remote_updated_at timestamptz,
    -- 00065 pushed-progress dedup
    hardcover_pushed_progress   integer,
    -- 00069 curation override layer
    status_override             text,
    rating_override             numeric,
    finished_at_override        timestamptz,
    curation_updated_at         timestamptz,
    hardcover_pushed_status     text,
    -- 00070 deep-link slug
    hardcover_slug              text,
    -- 00071 negative/attempt cache
    match_attempted_at          timestamptz,
    -- 00077 hardcover list memberships
    hardcover_lists             jsonb,
    -- 00080 idempotent finish-push id
    hardcover_read_id           bigint,
    UNIQUE (owner, source, external_id)
);
CREATE INDEX IF NOT EXISTS reading_items_owner_idx
    ON public.reading_items (owner);
CREATE INDEX IF NOT EXISTS reading_items_hardcover_book_idx
    ON public.reading_items (owner, hardcover_book_id)
    WHERE hardcover_book_id IS NOT NULL;

-- ---- reading_activity (00061) ----------------------------------------------
CREATE TABLE IF NOT EXISTS public.reading_activity (
    id                bigserial   PRIMARY KEY,
    owner             text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source            text        NOT NULL,
    granularity       text        NOT NULL DEFAULT 'day',
    bucket_date       date        NOT NULL,
    listening_seconds bigint      NOT NULL DEFAULT 0,
    pages             integer,
    synced_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, source, bucket_date, granularity)
);
CREATE INDEX IF NOT EXISTS reading_activity_owner_idx
    ON public.reading_activity (owner);

-- ---- book_sync_state (00062 + 00064) ---------------------------------------
CREATE TABLE IF NOT EXISTS public.book_sync_state (
    owner                text NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source               text NOT NULL,
    last_library_cursor  timestamptz,
    last_finished_cursor timestamptz,
    last_activity_cursor date,
    last_backfill_at     timestamptz,
    last_forward_at      timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    last_match_at        timestamptz,
    PRIMARY KEY (owner, source)
);

-- ---- hardcover_match_cache (00066 + 00070) — GLOBAL, no owner ---------------
CREATE TABLE IF NOT EXISTS hardcover_match_cache (
    id_type              text   NOT NULL CHECK (id_type IN ('asin','isbn13')),
    external_id          text   NOT NULL,
    hardcover_book_id    bigint NOT NULL,
    hardcover_edition_id bigint,
    method               text   NOT NULL,
    matched_at           timestamptz NOT NULL DEFAULT now(),
    book_slug            text,
    PRIMARY KEY (id_type, external_id)
);

-- ---- kindle_reading_insights (00067) ---------------------------------------
CREATE TABLE IF NOT EXISTS public.kindle_reading_insights (
    owner      text        PRIMARY KEY REFERENCES public.users(username) ON DELETE CASCADE,
    raw        jsonb       NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now()
);

-- ---- kindle_reading_positions (00068) --------------------------------------
CREATE TABLE IF NOT EXISTS public.kindle_reading_positions (
    id         bigserial   PRIMARY KEY,
    owner      text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    asin       text        NOT NULL,
    position   bigint      NOT NULL,
    sampled_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, asin, sampled_at)
);
CREATE INDEX IF NOT EXISTS kindle_reading_positions_owner_asin_sampled_idx
    ON public.kindle_reading_positions (owner, asin, sampled_at);

-- ---- kindle_reading_monitor_state (00072) ----------------------------------
CREATE TABLE IF NOT EXISTS public.kindle_reading_monitor_state (
    owner           text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    asin            text        NOT NULL,
    last_location   bigint      NOT NULL DEFAULT 0,
    last_advance_at timestamptz,
    last_polled_at  timestamptz,
    active          boolean     NOT NULL DEFAULT false,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner, asin)
);
CREATE INDEX IF NOT EXISTS kindle_reading_monitor_state_active_idx
    ON public.kindle_reading_monitor_state (owner) WHERE active;

-- ---- kindle_reading_monitor_advances (00073) -------------------------------
CREATE TABLE IF NOT EXISTS public.kindle_reading_monitor_advances (
    id            bigserial        PRIMARY KEY,
    owner         text             NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    source        text             NOT NULL DEFAULT 'kindle',
    interval_secs double precision NOT NULL,
    dloc          bigint           NOT NULL,
    at            timestamptz      NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS kindle_reading_monitor_advances_owner_at_idx
    ON public.kindle_reading_monitor_advances (owner, at DESC);

-- ---- hardcover_user_shelf (00074) ------------------------------------------
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

-- ---- reading_events (00078) — owner already FK-free in host -----------------
CREATE TABLE IF NOT EXISTS public.reading_events (
    id                bigserial   PRIMARY KEY,
    owner             text        NOT NULL,
    source            text        NOT NULL DEFAULT '',
    external_id       text        NOT NULL DEFAULT '',
    hardcover_book_id bigint,
    origin            text        NOT NULL,
    external_read_id  text        NOT NULL,
    started_at        timestamptz,
    finished_at       timestamptz,
    progress_pages    int,
    progress_seconds  int,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner, origin, external_read_id)
);
CREATE INDEX IF NOT EXISTS reading_events_owner_book_idx
    ON public.reading_events (owner, hardcover_book_id);
CREATE INDEX IF NOT EXISTS reading_events_owner_ext_idx
    ON public.reading_events (owner, source, external_id);

-- ---- notifications (00079) — owner already FK-free in host ------------------
CREATE TABLE IF NOT EXISTS public.notifications (
    id         bigserial   PRIMARY KEY,
    owner      text        NOT NULL,
    type       text        NOT NULL,
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    data       jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz
);
CREATE INDEX IF NOT EXISTS idx_notifications_owner_created
    ON public.notifications (owner, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.notifications;
DROP TABLE IF EXISTS public.reading_events;
DROP TABLE IF EXISTS public.hardcover_user_shelf;
DROP TABLE IF EXISTS public.kindle_reading_monitor_advances;
DROP TABLE IF EXISTS public.kindle_reading_monitor_state;
DROP TABLE IF EXISTS public.kindle_reading_positions;
DROP TABLE IF EXISTS public.kindle_reading_insights;
DROP TABLE IF EXISTS hardcover_match_cache;
DROP TABLE IF EXISTS public.book_sync_state;
DROP TABLE IF EXISTS public.reading_activity;
DROP TABLE IF EXISTS public.reading_items;
DROP TABLE IF EXISTS public.users;
-- +goose StatementEnd
