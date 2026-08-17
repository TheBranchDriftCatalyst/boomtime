-- 00077_hardcover_lists.sql — a book's Hardcover LIST memberships, stored as a
-- jsonb array of list names ON the reading_item (a property of the book, exactly
-- like `genres`). Many-to-many: a book can be on several lists. NULL until the
-- Hardcover pull attaches them. All editions of a Work share the same lists (the
-- pull writes by hardcover_book_id). Powers the `list` group-by/filter axis + the
-- list chips in the Book detail panel.
-- +goose Up
ALTER TABLE public.reading_items
    ADD COLUMN IF NOT EXISTS hardcover_lists jsonb;

-- +goose Down
ALTER TABLE public.reading_items
    DROP COLUMN IF EXISTS hardcover_lists;
