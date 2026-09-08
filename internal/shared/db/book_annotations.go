// book_annotations.go: the siloed ANNOTATION CORPUS (boom-siwi.5) — Kindle
// highlights/notes and, from phase 2, Audible clips/bookmarks. Like reading_items
// / reading_activity it does NOT write into heartbeats/stats/any core model; it
// cascade-deletes with the user. See migrations/00086 (host) and books 00006.
//
// The file enforces ONE contract the schema can only describe: the Amazon layer
// and the derived-transcript layer are written by DISJOINT accessors.
// UpsertBookAnnotation's ON CONFLICT names no transcript column, and
// SetBookAnnotationTranscript names nothing else. That is what stops a whisper
// re-run from overwriting words a human actually selected, and it mirrors the
// same split reading_items already draws between its ingest layer and its
// curation-override layer.
package db

import (
	"context"
	"time"
)

// Annotation kinds. Kindle produces highlight/note; Audible produces clip/bookmark.
const (
	AnnotationKindHighlight = "highlight"
	AnnotationKindNote      = "note"
	AnnotationKindBookmark  = "bookmark"
	AnnotationKindClip      = "clip"
)

// Position units. DECLARED per row, never inferred from source: a reflowable
// Kindle book reports a location while a print replica reports a page, and the
// clip cutter (boom-siwi.3) must be able to refuse anything that is not millis.
const (
	PositionUnitLocation = "location"
	PositionUnitPage     = "page"
	PositionUnitMillis   = "millis"
	PositionUnitPercent  = "percent"
)

// BookAnnotation is one highlight, note, bookmark or clip.
type BookAnnotation struct {
	ID            int64
	Owner         string
	Source        string // kindle | audible
	ExternalID    string // ASIN
	Kind          string
	AnnotationKey string // kind:start:end — the idempotency key

	PositionUnit  string
	PositionStart int64
	PositionEnd   *int64 // nil for point annotations (note, bookmark)

	// From Amazon.
	Body string
	Note string

	// Derived by us (boom-siwi.3). Never written by the annotation ingest.
	Transcript       string
	TranscriptSource string
	TranscriptAt     *time.Time

	CapturedAt      *time.Time // Amazon creationTime — when the annotation was MADE
	SourceUpdatedAt *time.Time
	RawMeta         []byte
}

// UpsertBookAnnotation inserts or refreshes one annotation, keyed by
// (owner, source, external_id, annotation_key) so re-running a sweep updates in
// place instead of duplicating.
//
// A no-op (nil) when annotation_key or position_unit is empty: a row with no
// idempotency key would duplicate on every sync, and a row with no declared unit
// is a position nobody can interpret. Returns whether a row was inserted or
// updated.
//
// The ON CONFLICT SET deliberately OMITS transcript, transcript_source and
// transcript_at. Adding them here would let an Amazon re-sync wipe a transcript
// that cost real GPU time to produce — see the file header.
func (d *DB) UpsertBookAnnotation(ctx context.Context, a BookAnnotation) (bool, error) {
	if a.AnnotationKey == "" || a.PositionUnit == "" {
		return false, nil
	}
	tag, err := d.Pool.Exec(ctx,
		`INSERT INTO book_annotations
		   (owner, source, external_id, kind, annotation_key,
		    position_unit, position_start, position_end,
		    body, note, captured_at, source_updated_at, raw_meta, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, now())
		 ON CONFLICT (owner, source, external_id, annotation_key) DO UPDATE SET
		   kind              = EXCLUDED.kind,
		   position_unit     = EXCLUDED.position_unit,
		   position_start    = EXCLUDED.position_start,
		   position_end      = EXCLUDED.position_end,
		   body              = EXCLUDED.body,
		   note              = EXCLUDED.note,
		   captured_at       = COALESCE(EXCLUDED.captured_at, book_annotations.captured_at),
		   source_updated_at = COALESCE(EXCLUDED.source_updated_at, book_annotations.source_updated_at),
		   raw_meta          = EXCLUDED.raw_meta,
		   deleted_at        = NULL,
		   updated_at        = now()`,
		a.Owner, a.Source, a.ExternalID, a.Kind, a.AnnotationKey,
		a.PositionUnit, a.PositionStart, a.PositionEnd,
		a.Body, a.Note, a.CapturedAt, a.SourceUpdatedAt, a.RawMeta)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetBookAnnotationTranscript writes ONLY the derived-transcript layer for one
// annotation (boom-siwi.3). It names no Amazon column, so a transcription can
// never clobber the highlighted passage or the user's own note — the mirror of
// the omission in UpsertBookAnnotation.
func (d *DB) SetBookAnnotationTranscript(ctx context.Context, owner, source, externalID, annotationKey, transcript, transcriptSource string) (bool, error) {
	tag, err := d.Pool.Exec(ctx,
		`UPDATE book_annotations
		    SET transcript = $5, transcript_source = $6, transcript_at = now(), updated_at = now()
		  WHERE owner = $1 AND source = $2 AND external_id = $3 AND annotation_key = $4`,
		owner, source, externalID, annotationKey, transcript, transcriptSource)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListBookAnnotations returns one book's live annotations in reading order —
// the detail-sheet read. Matches book_annotations_owner_book_pos_idx exactly.
// Tombstoned rows are excluded.
func (d *DB) ListBookAnnotations(ctx context.Context, owner, source, externalID string) ([]BookAnnotation, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT id, owner, source, external_id, kind, annotation_key,
		        position_unit, position_start, position_end,
		        body, note, transcript, transcript_source, transcript_at,
		        captured_at, source_updated_at, raw_meta
		   FROM book_annotations
		  WHERE owner = $1 AND ($2 = '' OR source = $2) AND external_id = $3
		    AND deleted_at IS NULL
		  ORDER BY position_start, id`,
		owner, source, externalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBookAnnotations(rows)
}

// ListBookAnnotationKeys returns the annotation keys already stored for one book.
// The sweep diffs the fetched set against this to decide what to tombstone.
func (d *DB) ListBookAnnotationKeys(ctx context.Context, owner, source, externalID string) ([]string, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT annotation_key FROM book_annotations
		  WHERE owner = $1 AND source = $2 AND external_id = $3 AND deleted_at IS NULL`,
		owner, source, externalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// TombstoneMissingBookAnnotations soft-deletes a book's stored annotations that
// were NOT in the latest fetch.
//
// The `len(seenKeys) == 0` guard is the whole point of the function and must not
// be relaxed. Amazon publishes no delete signal, so absence is the only evidence
// we ever get — and a parser that has broken against a changed DOM produces
// exactly the same evidence as a user who deleted everything. Preferring a stale
// row over a mass deletion is the right trade for a corpus, and it is the same
// posture the Kindle ingest takes with statusFromPercent's `known` return
// (boom-o6q5): structural silence is not information.
//
// A caller with a genuinely empty book should simply not call this.
func (d *DB) TombstoneMissingBookAnnotations(ctx context.Context, owner, source, externalID string, seenKeys []string) (int64, error) {
	if len(seenKeys) == 0 {
		return 0, nil
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE book_annotations
		    SET deleted_at = now(), updated_at = now()
		  WHERE owner = $1 AND source = $2 AND external_id = $3
		    AND deleted_at IS NULL
		    AND annotation_key <> ALL($4)`,
		owner, source, externalID, seenKeys)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountBookAnnotations returns a user's live annotation count per kind — what the
// admin/diagnostic surfaces render, and the cheap check that the first real sync
// landed the counts the probe predicted.
func (d *DB) CountBookAnnotations(ctx context.Context, owner, source string) (map[string]int, error) {
	rows, err := d.Pool.Query(ctx,
		`SELECT kind, count(*) FROM book_annotations
		  WHERE owner = $1 AND ($2 = '' OR source = $2) AND deleted_at IS NULL
		  GROUP BY kind`,
		owner, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	return out, rows.Err()
}

// DeleteBookAnnotations hard-deletes a user's annotations (source=="" → all) —
// the "delete my book data" path, mirroring DeleteKindleReadingPositions. This is
// the user asking, so unlike the tombstone reconcile it is unconditional.
func (d *DB) DeleteBookAnnotations(ctx context.Context, owner, source string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`DELETE FROM book_annotations WHERE owner = $1 AND ($2 = '' OR source = $2)`, owner, source)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// scanBookAnnotations is the shared row scan for the list queries.
func scanBookAnnotations(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]BookAnnotation, error) {
	var out []BookAnnotation
	for rows.Next() {
		var a BookAnnotation
		if err := rows.Scan(&a.ID, &a.Owner, &a.Source, &a.ExternalID, &a.Kind, &a.AnnotationKey,
			&a.PositionUnit, &a.PositionStart, &a.PositionEnd,
			&a.Body, &a.Note, &a.Transcript, &a.TranscriptSource, &a.TranscriptAt,
			&a.CapturedAt, &a.SourceUpdatedAt, &a.RawMeta); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
