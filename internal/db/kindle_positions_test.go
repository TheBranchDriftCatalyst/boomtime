// kindle_positions_test.go — pins the Kindle position-sample store: insert is
// idempotent per (owner, asin, sampled_at), ListKindleReadingPositions honors the
// `since` filter + ordering, and the reading_activity upsert the reading-time
// composition writes is idempotent (recompute → overwrite, never double-count).
package db

import (
	"context"
	"testing"
	"time"
)

func TestKindleReadingPositions_InsertIdempotentAndListed(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("kindle_pos")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)
	t.Cleanup(func() { _, _ = d.DeleteKindleReadingPositions(ctx, owner, "") })

	asin := "B0KINDLE01"
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	// First insert of a (owner, asin, sampled_at) → new row.
	ins, err := d.InsertKindleReadingPosition(ctx, owner, asin, 100, base)
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if !ins {
		t.Fatalf("first insert reported not-inserted")
	}
	// Same sampled_at again (even with a different position) → no-op (idempotent
	// capture: an accidental double-poll at the same instant must not duplicate).
	ins, err = d.InsertKindleReadingPosition(ctx, owner, asin, 999, base)
	if err != nil {
		t.Fatalf("insert dup: %v", err)
	}
	if ins {
		t.Errorf("duplicate-timestamp insert reported inserted (want no-op)")
	}

	// A later sample → new row.
	if _, err := d.InsertKindleReadingPosition(ctx, owner, asin, 250, base.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	// A much earlier sample (before the `since` cutoff we'll use).
	if _, err := d.InsertKindleReadingPosition(ctx, owner, asin, 10, base.Add(-48*time.Hour)); err != nil {
		t.Fatalf("insert old: %v", err)
	}

	// since = base-1h excludes the -48h row, keeps the two around `base`.
	got, err := d.ListKindleReadingPositions(ctx, owner, asin, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("since-filtered list = %d rows, want 2", len(got))
	}
	// Oldest-first ordering + the ON CONFLICT DO NOTHING kept the ORIGINAL 100
	// (not the 999 from the dup attempt).
	if got[0].Position != 100 || !got[0].SampledAt.Equal(base) {
		t.Errorf("row[0] = pos %d @ %v, want 100 @ %v (dup did not overwrite)", got[0].Position, got[0].SampledAt, base)
	}
	if got[1].Position != 250 {
		t.Errorf("row[1] position = %d, want 250", got[1].Position)
	}

	// Zero `since` includes the ancient row too.
	all, err := d.ListKindleReadingPositions(ctx, owner, asin, time.Time{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered list = %d rows, want 3", len(all))
	}
}

func TestReadingActivity_KindleUpsertIdempotent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("kindle_act")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)
	t.Cleanup(func() { _, _ = d.DeleteReadingActivity(ctx, owner, "") })

	day := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	act := ReadingActivity{Owner: owner, Source: "kindle", Granularity: "day", BucketDate: day, ListeningSeconds: 600}

	// First write.
	if err := d.UpsertReadingActivity(ctx, act); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	// Re-run of the composition recomputes the SAME value → overwrite, still one
	// row, same seconds (idempotent: no double-count).
	if err := d.UpsertReadingActivity(ctx, act); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	rows, err := d.ListReadingActivity(ctx, owner, "kindle", day, day)
	if err != nil {
		t.Fatalf("list activity: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("kindle buckets = %d, want 1 (upsert must not duplicate)", len(rows))
	}
	if rows[0].ListeningSeconds != 600 {
		t.Errorf("seconds = %d, want 600", rows[0].ListeningSeconds)
	}

	// A later recompute with MORE reading overwrites to the new total (not summed).
	act.ListeningSeconds = 900
	if err := d.UpsertReadingActivity(ctx, act); err != nil {
		t.Fatalf("upsert 3: %v", err)
	}
	rows, _ = d.ListReadingActivity(ctx, owner, "kindle", day, day)
	if len(rows) != 1 || rows[0].ListeningSeconds != 900 {
		t.Fatalf("after re-upsert: rows=%d seconds=%d, want 1 row @ 900", len(rows), rows[0].ListeningSeconds)
	}
}
