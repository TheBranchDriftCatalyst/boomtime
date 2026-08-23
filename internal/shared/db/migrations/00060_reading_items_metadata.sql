-- +goose Up
-- +goose StatementBegin

-- catalyst-audiobooks full-field ingest (boom-books): the Audible /1.0/library
-- item carries far more than title+authors. These columns are ADDITIVE (all
-- nullable or defaulted) so existing reading_items rows are untouched, and the
-- table stays siloed — it does NOT write into heartbeats/stats/any core model.
--   subtitle         — library subtitle
--   narrators        — narrators[].name CSV (audio-only)
--   series           — series[0].title (sequence kept in raw_meta)
--   runtime_min      — runtime_length_min (audio) / page-derived (kindle)
--   purchase_date    — library item purchase_date (forward-sync cursor source)
--   isbn             — isbn / amazon-side isbn (Hardcover match rung)
--   amazon_asin      — print/kindle sibling ASIN of the audio asin
--   genres           — category_ladders flattened → ["Fiction","Sci-Fi",…]
--   goodreads_rating — goodreads_ratings.rating (community avg, NOT the user's)
ALTER TABLE public.reading_items
  ADD COLUMN IF NOT EXISTS subtitle         text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS narrators        text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS series           text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS runtime_min      integer,
  ADD COLUMN IF NOT EXISTS purchase_date    timestamptz,
  ADD COLUMN IF NOT EXISTS isbn             text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS amazon_asin      text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS genres           jsonb,
  ADD COLUMN IF NOT EXISTS goodreads_rating numeric;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.reading_items
  DROP COLUMN IF EXISTS subtitle,
  DROP COLUMN IF EXISTS narrators,
  DROP COLUMN IF EXISTS series,
  DROP COLUMN IF EXISTS runtime_min,
  DROP COLUMN IF EXISTS purchase_date,
  DROP COLUMN IF EXISTS isbn,
  DROP COLUMN IF EXISTS amazon_asin,
  DROP COLUMN IF EXISTS genres,
  DROP COLUMN IF EXISTS goodreads_rating;
-- +goose StatementEnd
