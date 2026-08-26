-- +goose Up
-- +goose StatementBegin

-- 00005_liberation_giveup.sql — the standalone catalyst-books copy of the host's
-- liberation give-up state (host migration 00083). Identical: this one touches
-- only reading_items columns, so unlike 00004 there is no users FK to differ on.
--
-- LIBERATION GIVE-UP STATE (boom-w20s follow-up) — stop re-attempting titles
-- that will never succeed, and make the excluded set inspectable.
--
-- THE PROBLEM THIS FIXES. `failed` is a RETRYABLE status: ListUnliberated skips
-- only liberated/denied/unsupported_format/skipped, so anything sitting in
-- `failed` is re-requested by every future sweep, forever. That is correct for a
-- dropped connection and wrong for a title Amazon will refuse every single time.
-- Live proof: three podcasts (B08JJNKBZM, B08K56RYP6, B08K56VS1G) sat in
-- `failed` with "non_audio asset with contentDeliveryType:PodcastParent",
-- re-licensed on every sweep and failing identically each time.
--
--   liberation_attempts — CONSECUTIVE failed attempts. Incremented by
--                         MarkFailed, reset to 0 by MarkLiberated and by
--                         ClearLiberation (the "forget"/retry path). Once it
--                         reaches liberate.MaxAutoAttempts the sweep stops
--                         picking the row up, while an EXPLICIT single-title
--                         liberate still runs — giving up is a property of the
--                         unattended sweep, never a refusal to obey the user.
--
-- Why a counter on reading_items rather than a COUNT over
-- book_liberation_attempts: the same split migration 00082 already chose —
-- reading_items carries CURRENT state (and is the sweep's hot query), the
-- attempts table carries how we got there. A correlated subquery over a
-- forever-growing history table on every sweep is the wrong shape for it.
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS liberation_attempts integer NOT NULL DEFAULT 0;

-- Backfill the rows the code fix cannot reach. Classification happens when an
-- attempt RUNS, so titles that already failed keep their stale `failed` status
-- until something re-runs them — which is exactly the loop being closed here.
-- Matched on the error text because that is the only durable evidence left of
-- WHY they failed; both markers come from Amazon's own 400 body.
UPDATE public.reading_items
   SET liberation_status = 'unsupported_format'
 WHERE source = 'audible'
   AND liberation_status = 'failed'
   AND (liberation_error LIKE '%non_audio%' OR liberation_error LIKE '%PodcastParent%');

-- Partial index over the give-up set: "what did the sweep stop trying, and why"
-- is a question the UI asks on every load, and these rows are a tiny minority of
-- a large table.
CREATE INDEX IF NOT EXISTS reading_items_liberation_excluded_idx
    ON public.reading_items (owner, liberation_status)
    WHERE liberation_status IN ('denied', 'unsupported_format', 'unsupported_codec', 'skipped');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.reading_items_liberation_excluded_idx;
ALTER TABLE public.reading_items DROP COLUMN IF EXISTS liberation_attempts;
-- The status backfill is deliberately NOT reverted: putting podcasts back into
-- `failed` would restore the retry loop this migration exists to end.
-- +goose StatementEnd
