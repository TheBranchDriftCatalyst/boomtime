-- 00080_hardcover_read_id.sql — cache the Hardcover user_book_read id per reading
-- item so the finish-push is IDEMPOTENT. Without it, every finish push ran
-- insert_user_book_read (never update), spawning a NEW read on Hardcover each time
-- (click sync N times → N duplicate reads). With the cached id, subsequent pushes
-- update_user_book_read the SAME row. One read per book, updated in place.
-- +goose Up
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS hardcover_read_id bigint;

-- +goose Down
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS hardcover_read_id;
