-- 00006_book_annotations.sql — the ANNOTATION CORPUS (boom-siwi.5): Kindle
-- highlights/notes and Audible clips/bookmarks, in one table across both sources.
--
-- boom-siwi.1 established live where these come from, and the two sources look
-- nothing alike on the wire:
--
--   Audible   Fiona CDE sidecar, type=AUDI, ADP device-signed. JSON records of
--             type audible.clip (startPosition + endPosition) and
--             audible.bookmark (startPosition only).
--   Kindle    read.amazon.com/notebook over exchanged website cookies. HTML.
--             NOT the EBOK sidecar, which carries only kindle.lpr, and NOT
--             whispersync, whose non-shelf namespaces are device settings and
--             Vocabulary Builder decks.
--
-- ONE table rather than one per source, because every per-book table already
-- unifies across sources on (owner, source, external_id) — reading_items,
-- reading_activity, reading_events. A per-source split here would be the first
-- place the books domain breaks that, and each extra table costs a hand-written
-- entry in dumpTables, the standalone schema census, the wipe handler and every
-- fusion query.
--
-- Column notes, in the order the questions come up:
--
--   source/external_id  the book, keyed NATURALLY rather than by FK to
--                       reading_items.id. The notebook index can list a book
--                       whose Cloud Reader library row has not synced yet; an FK
--                       would drop that annotation on the floor.
--   annotation_key      the idempotency key: kind:start:end. Deliberately NOT
--                       Amazon's annotationId — the one example this repo has
--                       seen in full (sidecar.go: "<deviceId>-<ASIN>-EBOK-
--                       furthest-page-read") embeds the DEVICE SERIAL, so
--                       re-registering the Amazon device would mint a new serial
--                       and silently double the corpus. annotationId is kept in
--                       raw_meta for debugging. The cost of a positional key is
--                       that an EDITED highlight whose range moved lands as a new
--                       row and orphans the old one, which is why the ingest
--                       ships with a tombstone reconcile.
--   position_unit       DECLARED, never inferred from source. Kindle is not one
--                       unit: a reflowable book reports a location, a print
--                       replica reports a page. And boom-siwi.3's ffmpeg cutter
--                       must be able to REFUSE anything that is not millis —
--                       that guard is unwritable against an implicit unit.
--                       Offsets are stored verbatim, so if the millisecond
--                       reading of Audible's positions turns out wrong the fix
--                       is an UPDATE of this column, not of every value.
--   body/note           what Amazon said. Written only by the annotation ingest.
--   transcript*         what WE derived (boom-siwi.3, whisper over a clip cut
--                       from the liberated M4B). Disjoint from body/note so a
--                       re-transcription can never overwrite words a human
--                       actually selected, and so the fusion engine can weight a
--                       machine guess differently from ground truth. The
--                       accessors enforce the split: the Amazon upsert's ON
--                       CONFLICT names no transcript column, and the transcript
--                       setter names nothing else.
--   captured_at         Amazon's creationTime — when the annotation was MADE.
--                       This is the event timestamp the query domain buckets on;
--                       a highlight is a timestamped act of reading.
--   deleted_at          soft delete. Amazon gives no delete signal, so absence is
--                       the only evidence, and acting on absence is exactly how a
--                       broken HTML parser wipes a corpus. The reconcile that
--                       writes this is guarded to never fire on an empty fetch.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.book_annotations (
    id                bigserial   PRIMARY KEY,
    owner             text        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,

    source            text        NOT NULL,
    external_id       text        NOT NULL,
    kind              text        NOT NULL,
    annotation_key    text        NOT NULL,

    position_unit     text        NOT NULL,
    position_start    bigint      NOT NULL,
    position_end      bigint,

    body              text        NOT NULL DEFAULT '',
    note              text        NOT NULL DEFAULT '',

    transcript        text        NOT NULL DEFAULT '',
    transcript_source text        NOT NULL DEFAULT '',
    transcript_at     timestamptz,

    captured_at       timestamptz,
    source_updated_at timestamptz,
    raw_meta          jsonb,
    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    UNIQUE (owner, source, external_id, annotation_key)
);

-- The detail-sheet read: every live annotation for one book, in reading order.
CREATE INDEX IF NOT EXISTS book_annotations_owner_book_pos_idx
    ON public.book_annotations (owner, source, external_id, position_start)
    WHERE deleted_at IS NULL;

-- boom-siwi.3's work queue: clips still needing a cut + transcribe. Partial
-- because in the steady state almost every clip IS transcribed, so a full index
-- would be dead weight.
CREATE INDEX IF NOT EXISTS book_annotations_pending_transcript_idx
    ON public.book_annotations (owner, source, external_id)
    WHERE kind = 'clip' AND transcript = '' AND deleted_at IS NULL;

-- NOTE for phase 2 (Audible clips): that sweep is one sidecar call per candidate
-- book with no index endpoint to scope it, so it needs a rotating watermark
-- column on reading_items (annotations_checked_at, mirroring
-- match_attempted_at). It is deliberately NOT added here — this migration ships
-- the Kindle MVP, and an unused column would still owe an entry in dumpTables.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.book_annotations;
-- +goose StatementEnd
