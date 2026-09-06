// monitor_pollset_test.go — DB-backed coverage of the reading monitor's POLL
// SET (audit boom-l827). listInProgressKindle used to filter on the raw
// Amazon-derived it.Status, ignoring the curation override layer that the rest
// of the books surface treats as authoritative (toReadingItemDTO renders
// EffectiveStatus, and the Hardcover curation push mirrors it).
//
// The user-visible consequence ran both ways: a book the user marked 'reading'
// (Amazon still says 'want' because they read it on another device profile)
// showed as reading on the Books page but was never polled — open the socket,
// read the book, see no samples, conclude the whispersync probe is broken —
// while a derived-'reading' book overridden to 'dnf' burned a sidecar call every
// cycle forever.
package admin

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// seedKindleItem upserts a kindle reading_items row with the given DERIVED
// status, then (when override is non-empty) stamps the curation override on top
// — the same two layers production writes from the ingest and the PATCH.
func seedKindleItem(t *testing.T, database *db.DB, owner, asin, derived, override string) {
	t.Helper()
	ctx := context.Background()
	if err := database.UpsertReadingItem(ctx, db.ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: asin,
		Title: "Book " + asin, Authors: "A. Author", Status: derived,
	}); err != nil {
		t.Fatalf("seed %s: %v", asin, err)
	}
	if override == "" {
		return
	}
	if _, err := database.SetReadingItemCuration(ctx, owner, "kindle", asin,
		db.ReadingItemCurationPatch{SetStatus: true, Status: &override}); err != nil {
		t.Fatalf("override %s -> %s: %v", asin, override, err)
	}
}

// TestListInProgressKindle_UsesEffectiveStatusNotDerived drives the poll set
// through a real Postgres row set carrying both layers.
func TestListInProgressKindle_UsesEffectiveStatusNotDerived(t *testing.T) {
	hz := testutil.NewHarness(t) // skips when the isolated test DB is unreachable
	database := hz.DB
	ctx := context.Background()

	// MintUser registers the row-cleanup; reading_items cascades with the user.
	owner, _ := hz.MintUser("bookmon_pollset")
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(context.Background(), `DELETE FROM reading_items WHERE owner=$1`, owner)
	})

	// derived 'want', user curated it to 'reading' — the finding's case.
	seedKindleItem(t, database, owner, "B0CURATEDIN", "want", "reading")
	// derived 'reading', user curated it to 'dnf' — the wasted-poll case.
	seedKindleItem(t, database, owner, "B0CURATEDOUT", "reading", "dnf")
	// derived 'reading', untouched — must keep being polled.
	seedKindleItem(t, database, owner, "B0PLAINREAD", "reading", "")
	// derived 'want', untouched — must stay out.
	seedKindleItem(t, database, owner, "B0PLAINWANT", "want", "")
	// derived 'reading' but NO asin — unpollable, must stay dropped.
	seedKindleItem(t, database, owner, "", "reading", "")

	h := New(database, &config.Config{}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	items, err := h.listInProgressKindle(ctx, owner, 50)
	if err != nil {
		t.Fatalf("listInProgressKindle: %v", err)
	}

	got := map[string]bool{}
	for _, it := range items {
		got[it.ExternalID] = true
	}
	if !got["B0CURATEDIN"] {
		t.Error("a book curated to 'reading' (Amazon-derived 'want') is not in the poll set — " +
			"the Books page shows it as reading but the monitor never samples it")
	}
	if got["B0CURATEDOUT"] {
		t.Error("a book curated to 'dnf' (Amazon-derived 'reading') is still polled every cycle")
	}
	if !got["B0PLAINREAD"] {
		t.Error("an uncurated derived-'reading' book fell out of the poll set")
	}
	if got["B0PLAINWANT"] {
		t.Error("an uncurated derived-'want' book leaked into the poll set")
	}
	if got[""] {
		t.Error("a book with no ASIN is unpollable and must be dropped")
	}
	if len(items) != 2 {
		t.Errorf("poll set = %d books, want exactly 2 (B0CURATEDIN, B0PLAINREAD); got %v", len(items), got)
	}
}
