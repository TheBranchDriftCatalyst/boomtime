// kindle_insights_test.go — pins the Kindle Reading-Insights DB surface: the
// finish-DATE backfill onto kindle reading_items (COALESCE guard) and the raw
// snapshot upsert. These are integration tests (real Postgres via openTestDB).
package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestSetReadingItemFinishedFromInsights pins the finish-date backfill:
//   - a matching kindle row with NULL finished_at gets dated + flipped to
//     read/finished (newlyDated=true).
//   - a re-run with a DIFFERENT date does NOT clobber the existing date
//     (COALESCE) and reports newlyDated=false.
//   - a non-existent ASIN is a no-op (found=false), NOT a row creation.
//   - an audible row with the same ASIN is never touched (source-scoped).
func TestSetReadingItemFinishedFromInsights(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("kinsight")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// A kindle row that synced from the library feed with NO finish date (the
	// reality the insights backfill fixes) — currently mid-read.
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B00KINSIGHT",
		Title: "Dated Book", Authors: "A. Author", Status: "reading", ProgressPercent: 40,
	}); err != nil {
		t.Fatalf("upsert kindle row: %v", err)
	}
	// A same-ASIN audible row must stay untouched (source-scoped update).
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "audible", ExternalID: "B00KINSIGHT",
		Title: "Same ASIN Audio", Authors: "A. Author", Status: "reading", ProgressPercent: 10,
	}); err != nil {
		t.Fatalf("upsert audible row: %v", err)
	}

	// 1. First insights date: newly set, row flips to read/finished.
	firstDate := time.Date(2021, 6, 15, 0, 0, 0, 0, time.UTC)
	newly, found, err := d.SetReadingItemFinishedFromInsights(ctx, owner, "B00KINSIGHT", firstDate)
	if err != nil {
		t.Fatalf("SetReadingItemFinishedFromInsights (first): %v", err)
	}
	if !found || !newly {
		t.Fatalf("first backfill: found=%v newlyDated=%v, want both true", found, newly)
	}

	items, err := d.ListReadingItems(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("ListReadingItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 kindle row, got %d", len(items))
	}
	got := items[0]
	if got.Status != "read" || !got.Finished {
		t.Fatalf("row should be read/finished after backfill: %+v", got)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(firstDate) {
		t.Fatalf("finished_at = %v, want %v", got.FinishedAt, firstDate)
	}

	// 2. Re-run with a DIFFERENT (later) date: COALESCE keeps the first date and
	// reports newlyDated=false — an existing richer date is never clobbered.
	secondDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newly, found, err = d.SetReadingItemFinishedFromInsights(ctx, owner, "B00KINSIGHT", secondDate)
	if err != nil {
		t.Fatalf("SetReadingItemFinishedFromInsights (second): %v", err)
	}
	if !found || newly {
		t.Fatalf("second backfill: found=%v newlyDated=%v, want found=true newlyDated=false", found, newly)
	}
	items, _ = d.ListReadingItems(ctx, owner, "kindle")
	if fa := items[0].FinishedAt; fa == nil || !fa.Equal(firstDate) {
		t.Fatalf("finished_at must NOT be clobbered: got %v, want %v", fa, firstDate)
	}

	// 3. Unknown ASIN: no-op, no row created.
	newly, found, err = d.SetReadingItemFinishedFromInsights(ctx, owner, "B00NONE", firstDate)
	if err != nil {
		t.Fatalf("SetReadingItemFinishedFromInsights (missing): %v", err)
	}
	if found || newly {
		t.Fatalf("missing ASIN: found=%v newlyDated=%v, want both false", found, newly)
	}
	if items, _ := d.ListReadingItems(ctx, owner, "kindle"); len(items) != 1 {
		t.Fatalf("no row should be created for an unknown ASIN, got %d", len(items))
	}

	// 4. The audible row with the same ASIN was never touched.
	aud, _ := d.ListReadingItems(ctx, owner, "audible")
	if len(aud) != 1 || aud[0].Finished || aud[0].FinishedAt != nil {
		t.Fatalf("same-ASIN audible row must be untouched: %+v", aud)
	}
}

// TestUpsertKindleReadingInsights pins the raw snapshot store: insert, read-back,
// idempotent overwrite, and the per-user wipe.
func TestUpsertKindleReadingInsights(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("ksnap")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	if _, ok, err := d.GetKindleReadingInsights(ctx, owner); err != nil || ok {
		t.Fatalf("no snapshot expected yet: ok=%v err=%v", ok, err)
	}

	raw1 := []byte(`{"goal_info":{"titles_read":[{"asin":"B1","date_read":"2020-01-01"}]},"current_daily_streak":{"duration":3}}`)
	if err := d.UpsertKindleReadingInsights(ctx, owner, raw1); err != nil {
		t.Fatalf("upsert snapshot: %v", err)
	}
	snap, ok, err := d.GetKindleReadingInsights(ctx, owner)
	if err != nil || !ok {
		t.Fatalf("get snapshot: ok=%v err=%v", ok, err)
	}
	if snap.FetchedAt.IsZero() {
		t.Fatal("fetched_at should be set")
	}
	// jsonb round-trips as canonicalized JSON — assert a field survived rather than
	// byte-equality.
	if !containsJSON(t, snap.Raw, "current_daily_streak") {
		t.Fatalf("snapshot raw missing expected key: %s", snap.Raw)
	}

	// Overwrite (idempotent upsert): the new body replaces the old.
	raw2 := []byte(`{"current_daily_streak":{"duration":9},"urcGatingWeblabTreatment":"T2"}`)
	if err := d.UpsertKindleReadingInsights(ctx, owner, raw2); err != nil {
		t.Fatalf("re-upsert snapshot: %v", err)
	}
	snap, _, _ = d.GetKindleReadingInsights(ctx, owner)
	if !containsJSON(t, snap.Raw, "urcGatingWeblabTreatment") {
		t.Fatalf("snapshot not overwritten: %s", snap.Raw)
	}

	// Wipe.
	n, err := d.DeleteKindleReadingInsights(ctx, owner)
	if err != nil || n != 1 {
		t.Fatalf("delete snapshot: n=%d err=%v", n, err)
	}
	if _, ok, _ := d.GetKindleReadingInsights(ctx, owner); ok {
		t.Fatal("snapshot should be gone after delete")
	}
}

// containsJSON reports whether the jsonb-stored body contains a top-level key,
// tolerant of jsonb's whitespace/key-order canonicalization.
func containsJSON(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v (%s)", err, raw)
	}
	_, ok := m[key]
	return ok
}
