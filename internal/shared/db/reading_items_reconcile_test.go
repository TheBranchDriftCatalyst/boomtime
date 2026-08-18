// reading_items_reconcile_test.go — pins SetReadingItemReading, the honest-status
// flip the Kindle status-reconcile writes: it promotes a non-read kindle row to
// 'reading' but MUST refuse to clobber a read/finished row and MUST be a no-op on
// a row that is already 'reading'. Integration test (real Postgres via
// openTestDB), mirroring kindle_insights_test.go.
package db

import (
	"context"
	"testing"
	"time"
)

func TestSetReadingItemReading(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("kreconcile")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// A fresh library row: ingest defaults it to 'want' (Cloud Reader reports 0%).
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0WANT01",
		Title: "Wanted Book", Status: "want",
	}); err != nil {
		t.Fatalf("upsert want row: %v", err)
	}
	// A finished/read row (insights already dated it) — the sweep must NEVER demote
	// it back to 'reading' on the strength of its end-of-book lpr.
	finishedAt := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0READ01",
		Title: "Finished Book", Status: "read", Finished: true, FinishedAt: &finishedAt,
	}); err != nil {
		t.Fatalf("upsert read row: %v", err)
	}
	// A same-ASIN audible row must stay untouched (source-scoped update).
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B0WANT01",
		Title: "Same ASIN Audio", Status: "want",
	}); err != nil {
		t.Fatalf("upsert audible row: %v", err)
	}

	// 1. want -> reading: a row actually changed.
	changed, err := d.SetReadingItemReading(ctx, owner, "kindle", "B0WANT01")
	if err != nil {
		t.Fatalf("SetReadingItemReading (want): %v", err)
	}
	if !changed {
		t.Fatal("want->reading should report changed=true")
	}

	// 2. Idempotent: a second call on the now-'reading' row is a no-op.
	changed, err = d.SetReadingItemReading(ctx, owner, "kindle", "B0WANT01")
	if err != nil {
		t.Fatalf("SetReadingItemReading (re-run): %v", err)
	}
	if changed {
		t.Fatal("already-reading row must report changed=false (no-op)")
	}

	// 3. Refuses to clobber a read/finished row.
	changed, err = d.SetReadingItemReading(ctx, owner, "kindle", "B0READ01")
	if err != nil {
		t.Fatalf("SetReadingItemReading (read): %v", err)
	}
	if changed {
		t.Fatal("read/finished row must NOT be touched (changed=false)")
	}

	// 4. A non-existent ASIN is a clean no-op, not an error.
	changed, err = d.SetReadingItemReading(ctx, owner, "kindle", "B0MISSING")
	if err != nil {
		t.Fatalf("SetReadingItemReading (missing): %v", err)
	}
	if changed {
		t.Fatal("absent row must report changed=false")
	}

	// Verify end state: the want row is 'reading', the read row is untouched, and
	// the same-ASIN audible row was never promoted.
	kindle, err := d.ListReadingItems(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("ListReadingItems kindle: %v", err)
	}
	statusByID := map[string]ReadingItem{}
	for _, it := range kindle {
		statusByID[it.ExternalID] = it
	}
	if got := statusByID["B0WANT01"].Status; got != "reading" {
		t.Fatalf("B0WANT01 status = %q, want reading", got)
	}
	read := statusByID["B0READ01"]
	if read.Status != "read" || !read.Finished {
		t.Fatalf("B0READ01 must stay read/finished, got status=%q finished=%v", read.Status, read.Finished)
	}
	if read.FinishedAt == nil || !read.FinishedAt.Equal(finishedAt) {
		t.Fatalf("B0READ01 finished_at must be preserved, got %v", read.FinishedAt)
	}

	audible, err := d.ListReadingItems(ctx, owner, "audible")
	if err != nil {
		t.Fatalf("ListReadingItems audible: %v", err)
	}
	if len(audible) != 1 || audible[0].Status != "want" {
		t.Fatalf("same-ASIN audible row must stay 'want' (source-scoped), got %+v", audible)
	}
}
