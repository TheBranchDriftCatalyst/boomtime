// hardcover_pull_test.go — pins the INBOUND reconcile write
// (UpdateHardcoverLinkFromPull, gaka-books): the Hardcover PULL updates ONLY the
// minimal linkage (hardcover_status + hardcover_remote_updated_at) on a
// reading_item already matched by hardcover_book_id, and leaves an unmatched book
// untouched (rows=0 → the caller's inbound-origin follow-up). Runs against the
// ephemeral pg harness.
package db

import (
	"context"
	"testing"
	"time"
)

func TestUpdateHardcoverLinkFromPull(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("hcpull")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	// Seed a reading_item and link it to a Hardcover book (as the match ladder
	// would have on an earlier push). hardcover_status starts unset.
	const bookID = int64(555)
	if err := d.UpsertReadingItem(ctx, ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: "B0PULL0001",
		Title: "Dune", Authors: "Frank Herbert", Status: "reading",
		ProgressPercent: 40,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=$3
		  WHERE owner=$1 AND source='kindle' AND external_id=$2`,
		owner, "B0PULL0001", bookID); err != nil {
		t.Fatalf("link: %v", err)
	}

	// Reconcile the pulled shelf state: status read + Hardcover's updated_at.
	remote := time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC)
	n, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "read", RemoteUpdatedAt: remote,
	})
	if err != nil {
		t.Fatalf("UpdateHardcoverLinkFromPull: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows updated = %d, want 1", n)
	}

	// Read it back via ListReadingItems and assert the linkage landed. The remote
	// updated_at isn't in that projection, so verify it directly.
	items, err := d.ListReadingItems(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].HardcoverStatus == nil || *items[0].HardcoverStatus != "read" {
		t.Fatalf("hardcover_status = %v, want read", items[0].HardcoverStatus)
	}

	var gotRemote time.Time
	if err := d.Pool.QueryRow(ctx,
		`SELECT hardcover_remote_updated_at FROM reading_items
		  WHERE owner=$1 AND external_id=$2`, owner, "B0PULL0001").Scan(&gotRemote); err != nil {
		t.Fatalf("scan remote updated_at: %v", err)
	}
	if !gotRemote.UTC().Equal(remote) {
		t.Fatalf("hardcover_remote_updated_at = %v, want %v", gotRemote.UTC(), remote)
	}

	// A book NOT linked to any local reading_item reconciles 0 rows — the
	// inbound-origin follow-up signal, not an error.
	n, err = d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: 999999, Status: "want", RemoteUpdatedAt: remote,
	})
	if err != nil {
		t.Fatalf("unmatched reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("unmatched rows updated = %d, want 0", n)
	}

	// An empty status must not blank the previously-set value (COALESCE guard).
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, HardcoverUserBookLink{
		BookID: bookID, Status: "", RemoteUpdatedAt: time.Time{},
	}); err != nil {
		t.Fatalf("empty-status reconcile: %v", err)
	}
	items, _ = d.ListReadingItems(ctx, owner, "kindle")
	if items[0].HardcoverStatus == nil || *items[0].HardcoverStatus != "read" {
		t.Fatalf("empty status must preserve prior 'read', got %v", items[0].HardcoverStatus)
	}
}
