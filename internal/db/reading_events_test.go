// reading_events_test.go — pins the multiple-reads model + its idempotency
// (migration 00078). Re-ingesting the SAME read (stable id) updates in place, never
// duplicates; two DIFFERENT reads of one book = two events; a re-read with a new
// finish = a new event. This is the "re-running the pipeline is idempotent" guarantee.
package db

import (
	"context"
	"testing"
	"time"
)

func TestUpsertReadingEvent_Idempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("revents")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	book := int64(990011)
	fin1 := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	fin2 := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)

	ev := func(readID string, fa time.Time) ReadingEvent {
		f := fa
		return ReadingEvent{
			Owner: owner, HardcoverBookID: &book, Origin: ReadingEventOriginHardcover,
			ExternalReadID: readID, FinishedAt: &f,
		}
	}

	// First read.
	if err := d.UpsertReadingEvent(ctx, ev("hc-1", fin1)); err != nil {
		t.Fatalf("read 1: %v", err)
	}
	// Re-ingest the SAME read (same stable id) twice — must NOT duplicate.
	for i := 0; i < 2; i++ {
		if err := d.UpsertReadingEvent(ctx, ev("hc-1", fin1)); err != nil {
			t.Fatalf("re-ingest read 1: %v", err)
		}
	}
	got, err := d.ListReadingEventsForWork(ctx, owner, &book, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("same read re-ingested → want 1 event, got %d", len(got))
	}

	// A SECOND, distinct read of the same book (a re-read) = a new event.
	if err := d.UpsertReadingEvent(ctx, ev("hc-2", fin2)); err != nil {
		t.Fatalf("read 2: %v", err)
	}
	got, _ = d.ListReadingEventsForWork(ctx, owner, &book, "", "")
	if len(got) != 2 {
		t.Fatalf("two distinct reads → want 2 events, got %d", len(got))
	}
	// Newest finish first (ordering).
	if got[0].FinishedAt == nil || !got[0].FinishedAt.Equal(fin2) {
		t.Errorf("want newest read (fin2) first, got %v", got[0].FinishedAt)
	}

	// Empty origin/external_read_id → no-op (no unkeyable rows).
	if err := d.UpsertReadingEvent(ctx, ReadingEvent{Owner: owner, HardcoverBookID: &book}); err != nil {
		t.Fatalf("unkeyed: %v", err)
	}
	got, _ = d.ListReadingEventsForWork(ctx, owner, &book, "", "")
	if len(got) != 2 {
		t.Fatalf("unkeyed event should be a no-op, got %d events", len(got))
	}
}
