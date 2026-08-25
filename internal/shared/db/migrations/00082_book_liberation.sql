-- +goose Up
-- +goose StatementBegin

-- catalyst-books LIBERATION state (boom-w20s) — the Libation rebuild.
--
-- Liberation turns an owned Audible title into a DRM-free M4B on a filesystem
-- library. This migration adds the state that makes a sweep IDEMPOTENT and its
-- failures DIAGNOSABLE.
--
-- WHY ON reading_items RATHER THAN A NEW TABLE. There is exactly one row per
-- owned title already, and the books Explorer reads that table — so putting the
-- current state here makes `liberation_status` a filterable column and a facet
-- for free. This mirrors the curation override layer (migration 00069), which
-- made the same call for the same reason.
--
--   liberation_status  — pending | licensing | downloading | converting |
--                        liberated | failed | denied | unsupported_codec |
--                        unsupported_format | skipped
--                        Plain text, not a PG enum: the additive-only rule means
--                        we can never ALTER TYPE, and a new state must not
--                        require a migration to a column that already exists.
--   audio_path         — path RELATIVE to BOOM_BOOKS_LIBRARY_PATH. Relative so
--                        the library can be re-mounted at a different path (or
--                        moved between hosts) without rewriting every row.
--   audio_bytes        — size at commit time. Paired with audio_path this is the
--                        idempotency check: present + matching size = skip;
--                        missing = re-liberate (deleted); mismatched =
--                        re-liberate (truncated).
--   content_format     — Audible's AAX_44_128 / AAX_22_64 / … Persisted so
--                        "how many titles are on a codec we cannot remux" is a
--                        SQL question rather than a log-scraping exercise. That
--                        count is the documented trigger for the native-decoder
--                        epic (design doc §10 epic D).
--   liberation_error   — last failure, user-visible in the detail sheet.
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS liberation_status text,
    ADD COLUMN IF NOT EXISTS liberated_at      timestamptz,
    ADD COLUMN IF NOT EXISTS audio_path        text,
    ADD COLUMN IF NOT EXISTS audio_bytes       bigint,
    ADD COLUMN IF NOT EXISTS audio_format      text,
    ADD COLUMN IF NOT EXISTS content_format    text,
    ADD COLUMN IF NOT EXISTS liberation_error  text;

-- Partial index: the sweep's hot query is "which of this owner's titles still
-- need liberating". Partial (WHERE liberation_status IS DISTINCT FROM
-- 'liberated') keeps it small as the library fills up — in the steady state
-- almost every row IS liberated, so a full index would be mostly dead weight.
CREATE INDEX IF NOT EXISTS reading_items_liberation_pending_idx
    ON public.reading_items (owner, liberation_status)
    WHERE liberation_status IS DISTINCT FROM 'liberated';

-- Attempt history. reading_items carries the CURRENT state; this carries how we
-- got there, so "why did this book fail three times last week" is answerable
-- without pulling job logs back out of MinIO.
CREATE TABLE IF NOT EXISTS public.book_liberation_attempts (
    id             bigserial   PRIMARY KEY,
    owner          text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    asin           text        NOT NULL,
    started_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    status         text        NOT NULL DEFAULT 'pending',
    bytes          bigint,
    duration_ms    bigint,
    content_format text,
    error          text
);

CREATE INDEX IF NOT EXISTS book_liberation_attempts_owner_started_idx
    ON public.book_liberation_attempts (owner, started_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.book_liberation_attempts;
DROP INDEX IF EXISTS public.reading_items_liberation_pending_idx;
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS liberation_status,
    DROP COLUMN IF EXISTS liberated_at,
    DROP COLUMN IF EXISTS audio_path,
    DROP COLUMN IF EXISTS audio_bytes,
    DROP COLUMN IF EXISTS audio_format,
    DROP COLUMN IF EXISTS content_format,
    DROP COLUMN IF EXISTS liberation_error;
-- +goose StatementEnd
