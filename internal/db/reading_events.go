package db

import (
	"context"
	"strings"
	"time"
)

// reading_events.go — discrete READ events (migration 00078). A book can be read
// more than once; each read is a row here (start/finish/progress), while
// reading_items keeps the current/latest snapshot. Re-ingest is idempotent: a read
// upserts by (owner, origin, external_read_id), so re-running the pipeline never
// duplicates a read. Hardcover is the authoritative multi-read source (its
// user_book_reads carry a stable id); an Amazon finish contributes one event.
//
// This is distinct from reading_activity (the per-day reading-seconds heartbeat
// layer, reading_activity.go) — events are discrete reads, activity is time.

// Reading-event origins.
const (
	ReadingEventOriginHardcover      = "hardcover"
	ReadingEventOriginAudible        = "audible"
	ReadingEventOriginKindleInsights = "kindle-insights"
)

// ReadingEvent is one discrete read of a book.
type ReadingEvent struct {
	Owner           string
	Source          string
	ExternalID      string
	HardcoverBookID *int64
	Origin          string
	ExternalReadID  string // the origin's stable id — the idempotency key
	StartedAt       *time.Time
	FinishedAt      *time.Time
	ProgressPages   *int
	ProgressSeconds *int
}

// UpsertReadingEvent inserts or refreshes one read, keyed by (owner, origin,
// external_read_id) so a re-ingested read updates in place rather than
// duplicating. A no-op (nil) when origin/external_read_id are empty (no idempotency
// key → we won't write an unkeyable row).
func (d *DB) UpsertReadingEvent(ctx context.Context, ev ReadingEvent) error {
	ev.Origin = strings.TrimSpace(ev.Origin)
	ev.ExternalReadID = strings.TrimSpace(ev.ExternalReadID)
	if ev.Origin == "" || ev.ExternalReadID == "" {
		return nil
	}
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO reading_events
		   (owner, source, external_id, hardcover_book_id, origin, external_read_id,
		    started_at, finished_at, progress_pages, progress_seconds, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		 ON CONFLICT (owner, origin, external_read_id) DO UPDATE SET
		    source           = EXCLUDED.source,
		    external_id      = EXCLUDED.external_id,
		    hardcover_book_id = COALESCE(EXCLUDED.hardcover_book_id, reading_events.hardcover_book_id),
		    started_at       = COALESCE(EXCLUDED.started_at, reading_events.started_at),
		    finished_at      = COALESCE(EXCLUDED.finished_at, reading_events.finished_at),
		    progress_pages   = COALESCE(EXCLUDED.progress_pages, reading_events.progress_pages),
		    progress_seconds = COALESCE(EXCLUDED.progress_seconds, reading_events.progress_seconds),
		    updated_at       = now()`,
		ev.Owner, ev.Source, ev.ExternalID, ev.HardcoverBookID, ev.Origin, ev.ExternalReadID,
		ev.StartedAt, ev.FinishedAt, ev.ProgressPages, ev.ProgressSeconds)
	return err
}

// ListReadingEventsForWork returns the read history for one canonical Work — the
// Book detail panel's "Reads" section. Matches by hardcover_book_id when set,
// falling back to source+external_id (an unmatched book's own reads). Newest
// finish first, then newest start. At least one identity must be given.
func (d *DB) ListReadingEventsForWork(ctx context.Context, owner string, hardcoverBookID *int64, source, externalID string) ([]ReadingEvent, error) {
	externalID = strings.TrimSpace(externalID)
	if hardcoverBookID == nil && externalID == "" {
		return nil, nil
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT owner, source, external_id, hardcover_book_id, origin, external_read_id,
		        started_at, finished_at, progress_pages, progress_seconds
		   FROM reading_events
		  WHERE owner = $1
		    AND ( ($2::bigint IS NOT NULL AND hardcover_book_id = $2)
		       OR ($3 <> '' AND external_id = $3) )
		  ORDER BY finished_at DESC NULLS LAST, started_at DESC NULLS LAST`,
		owner, hardcoverBookID, externalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadingEvent
	for rows.Next() {
		var ev ReadingEvent
		if err := rows.Scan(&ev.Owner, &ev.Source, &ev.ExternalID, &ev.HardcoverBookID,
			&ev.Origin, &ev.ExternalReadID, &ev.StartedAt, &ev.FinishedAt,
			&ev.ProgressPages, &ev.ProgressSeconds); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
