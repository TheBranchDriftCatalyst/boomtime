package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// hardcover_shelf_test.go — the local Hardcover shelf mirror (migration 00074):
// UpsertHardcoverShelfEntry inserts + refreshes (idempotent on
// owner+hardcover_book_id), and ListHardcoverShelf returns the owner's whole
// shelf. Non-tautological: it asserts the actual round-tripped rows, so a broken
// upsert/list column mapping fails here.

func TestHardcoverShelf_UpsertAndList(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("shelf_%d", time.Now().UnixNano())
	seedShelfUser(t, d, ctx, owner)
	t.Cleanup(func() { deleteSenderRows(d, ctx, owner) })

	up := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := d.UpsertHardcoverShelfEntry(ctx, owner, ShelfEntry{
		BookID: 900, Title: "Project Hail Mary", Author: "Andy Weir", Slug: "project-hail-mary", Status: "read",
	}, &up); err != nil {
		t.Fatalf("upsert entry 1: %v", err)
	}
	if err := d.UpsertHardcoverShelfEntry(ctx, owner, ShelfEntry{
		BookID: 901, Title: "Dune", Author: "Frank Herbert", Slug: "dune", Status: "reading",
	}, nil); err != nil {
		t.Fatalf("upsert entry 2 (nil updatedAt): %v", err)
	}

	shelf, err := d.ListHardcoverShelf(ctx, owner)
	if err != nil {
		t.Fatalf("list shelf: %v", err)
	}
	if len(shelf) != 2 {
		t.Fatalf("shelf size = %d, want 2: %+v", len(shelf), shelf)
	}
	byID := map[int64]ShelfEntry{}
	for _, e := range shelf {
		byID[e.BookID] = e
	}
	if got := byID[900]; got.Title != "Project Hail Mary" || got.Author != "Andy Weir" || got.Slug != "project-hail-mary" || got.Status != "read" {
		t.Fatalf("entry 900 round-trip = %+v", got)
	}
	if got := byID[901]; got.Title != "Dune" || got.Author != "Frank Herbert" || got.Status != "reading" {
		t.Fatalf("entry 901 round-trip = %+v", got)
	}

	// Re-upsert 900 with new status/author → overwrite, no duplicate row.
	if err := d.UpsertHardcoverShelfEntry(ctx, owner, ShelfEntry{
		BookID: 900, Title: "Project Hail Mary", Author: "A. Weir", Slug: "project-hail-mary", Status: "paused",
	}, &up); err != nil {
		t.Fatalf("re-upsert entry 1: %v", err)
	}
	shelf2, err := d.ListHardcoverShelf(ctx, owner)
	if err != nil {
		t.Fatalf("list shelf after re-upsert: %v", err)
	}
	if len(shelf2) != 2 {
		t.Fatalf("shelf size after re-upsert = %d, want 2 (upsert, not insert)", len(shelf2))
	}
	for _, e := range shelf2 {
		if e.BookID == 900 && (e.Status != "paused" || e.Author != "A. Weir") {
			t.Fatalf("entry 900 was not overwritten: %+v", e)
		}
	}
}

// TestHardcoverShelf_CascadesWithUser proves the FK cascade: deleting the user
// removes their shelf rows (the siloed-storage guarantee).
func TestHardcoverShelf_CascadesWithUser(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("shelf_casc_%d", time.Now().UnixNano())
	seedShelfUser(t, d, ctx, owner)

	if err := d.UpsertHardcoverShelfEntry(ctx, owner, ShelfEntry{BookID: 1, Title: "T"}, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, owner); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	shelf, err := d.ListHardcoverShelf(ctx, owner)
	if err != nil {
		t.Fatalf("list after cascade: %v", err)
	}
	if len(shelf) != 0 {
		t.Fatalf("shelf not cascaded on user delete: %d rows remain", len(shelf))
	}
}

func seedShelfUser(t *testing.T, d *DB, ctx context.Context, owner string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}
