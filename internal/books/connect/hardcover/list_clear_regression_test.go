// list_clear_regression_test.go — the missing red-verification for the
// sync.go:298 finding of the 2026-09-06 audit (epic boom-l827).
//
// The fix added a keep-set + ClearReadingItemListsExcept sweep to
// attachListMemberships, but the regression test that shipped with it
// (TestClearReadingItemListsExcept) exercises the DB HELPER, not the wiring.
// QA proved that: reverting the sync.go hunk left that test green, so nothing
// pinned the finding's actual symptom and a refactor could silently drop the
// call. This file tests the layer the finding names.
package hardcover

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubUserListsClient builds a Client whose transport answers every GraphQL call
// with body. Client.http is package-private, so an in-package test can inject a
// transport without needing an endpoint seam on the production type.
func stubUserListsClient(body string) *Client {
	c := NewClient("stub-token")
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	return c
}

func setHardcoverBookID(t *testing.T, d *db.DB, ctx context.Context, owner, externalID string, bookID int64) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=$3 WHERE owner=$1 AND external_id=$2`,
		owner, externalID, bookID); err != nil {
		t.Fatalf("set hardcover_book_id for %s: %v", externalID, err)
	}
}

func listsFor(t *testing.T, d *db.DB, ctx context.Context, owner, externalID string) string {
	t.Helper()
	var lists *string
	if err := d.Pool.QueryRow(ctx,
		`SELECT hardcover_lists::text FROM reading_items WHERE owner=$1 AND external_id=$2`,
		owner, externalID).Scan(&lists); err != nil {
		t.Fatalf("read lists for %s: %v", externalID, err)
	}
	if lists == nil {
		return "<null>"
	}
	return *lists
}

// TestAttachListMemberships_ClearsBooksRemovedFromEveryList drives the finding's
// exact scenario: a book that WAS on a Hardcover list is removed from all of
// them. SetReadingItemListsForBook only ever writes books PRESENT in the current
// membership map, so without the clear sweep the removed book keeps rendering
// its stale list chips forever.
//
// This goes red when the sync.go hunk is reverted, which is the property the
// shipped test lacked.
func TestAttachListMemberships_ClearsBooksRemovedFromEveryList(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("hc_listclear_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	const stillListed, deListed = int64(100), int64(200)

	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0STILL", Title: "Still Listed"})
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0GONE", Title: "Removed From Lists"})
	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0UNMATCHED", Title: "Never Matched"})
	setHardcoverBookID(t, d, ctx, owner, "B0STILL", stillListed)
	setHardcoverBookID(t, d, ctx, owner, "B0GONE", deListed)

	// An earlier pull put both matched books on a list.
	for _, id := range []int64{stillListed, deListed} {
		if err := d.SetReadingItemListsForBook(ctx, owner, id, []byte(`["Sci-Fi"]`)); err != nil {
			t.Fatalf("seed list membership for %d: %v", id, err)
		}
	}

	// Hardcover now reports only book 100 on any list — 200 was removed from all.
	client := stubUserListsClient(
		`{"data":{"lists":[{"id":7,"name":"Sci-Fi","books_count":1,"list_books":[{"book_id":100}]}]}}`)
	s := NewSyncService(d, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	listed, err := s.attachListMemberships(ctx, owner, client, 42)
	if err != nil {
		t.Fatalf("attachListMemberships: %v", err)
	}
	if listed != 1 {
		t.Errorf("listed = %d, want 1 (only book %d is still on a list)", listed, stillListed)
	}

	if got := listsFor(t, d, ctx, owner, "B0STILL"); got != `["Sci-Fi"]` {
		t.Errorf("still-listed book lost its membership: hardcover_lists = %s, want [\"Sci-Fi\"]", got)
	}
	if got := listsFor(t, d, ctx, owner, "B0GONE"); got != `[]` {
		t.Errorf("book removed from every Hardcover list still renders hardcover_lists = %s, want [] — "+
			"attachListMemberships never cleared the stale membership, so the UI shows list chips "+
			"for a list the book is no longer on", got)
	}
	// A row that was never matched has no hardcover_book_id and must not be touched.
	if got := listsFor(t, d, ctx, owner, "B0UNMATCHED"); got != "<null>" && got != `[]` {
		t.Errorf("unmatched row was modified by the clear sweep: hardcover_lists = %s", got)
	}
}

// TestAttachListMemberships_EmptyMembershipClearsEverything covers the boundary
// the sweep is easiest to get wrong: the user removed ALL of their lists. The
// keep set is empty, which must clear every listed row rather than being read as
// "keep everything".
func TestAttachListMemberships_EmptyMembershipClearsEverything(t *testing.T) {
	d := openSweepDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("hc_listnone_%d", time.Now().UnixNano())
	seedUser(t, d, ctx, owner)
	t.Cleanup(func() { cleanupOwner(d, ctx, owner) })

	mustUpsert(t, d, ctx, db.ReadingItem{Owner: owner, Source: "kindle", ExternalID: "B0ONLY", Title: "Only Book"})
	setHardcoverBookID(t, d, ctx, owner, "B0ONLY", 300)
	if err := d.SetReadingItemListsForBook(ctx, owner, 300, []byte(`["Sci-Fi","To Read"]`)); err != nil {
		t.Fatalf("seed list membership: %v", err)
	}

	client := stubUserListsClient(`{"data":{"lists":[]}}`)
	s := NewSyncService(d, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := s.attachListMemberships(ctx, owner, client, 42); err != nil {
		t.Fatalf("attachListMemberships: %v", err)
	}
	if got := listsFor(t, d, ctx, owner, "B0ONLY"); got != `[]` {
		t.Errorf("user deleted every Hardcover list but the book still renders %s, want []", got)
	}
}
