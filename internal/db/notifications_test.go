package db

import (
	"context"
	"testing"
	"time"
)

// TestNotifications_SaveListMarkRead round-trips the durable-notification store:
// save two, list newest-first with an unread count, mark all read → unread 0.
func TestNotifications_SaveListMarkRead(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	owner := mkSender("notif")
	cleanupSender(t, d, ctx, owner)
	ensureUser(t, d, ctx, owner)

	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if err := d.SaveNotification(ctx, owner, "book.finished", "Finished: Dune", "Frank Herbert",
		map[string]any{"asin": "B01"}, t0); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := d.SaveNotification(ctx, owner, "book.finished", "Finished: Hail Mary", "Andy Weir", nil, t1); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	items, unread, err := d.ListNotifications(ctx, owner, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 || unread != 2 {
		t.Fatalf("want 2 items / 2 unread, got %d / %d", len(items), unread)
	}
	if items[0].Title != "Finished: Hail Mary" { // newest first
		t.Errorf("order wrong: first = %q", items[0].Title)
	}
	if items[1].Data["asin"] != "B01" {
		t.Errorf("data not round-tripped: %v", items[1].Data)
	}

	n, err := d.MarkNotificationsRead(ctx, owner)
	if err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if n != 2 {
		t.Fatalf("marked %d, want 2", n)
	}
	_, unread, _ = d.ListNotifications(ctx, owner, 50)
	if unread != 0 {
		t.Fatalf("unread after mark = %d, want 0", unread)
	}
}
