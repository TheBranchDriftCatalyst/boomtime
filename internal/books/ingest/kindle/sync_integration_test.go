// sync_integration_test.go — DB-backed (integration) proof of the Kindle
// ingest→reading_items write path (gaka-c8a9). The unit tests (ingest_test.go)
// exercise the pure `sweep`/`buildReadingItem` mapping with fakes; this pins the
// half they can't reach: SyncUser actually UPSERTING rows into
// reading_items(source='kindle') on a real Postgres, and the IDEMPOTENCY
// contract — a re-run over an unchanged library re-upserts the SAME rows with no
// duplicates, and a changed percentageRead UPDATES the existing row in place
// (keyed by owner+source+external_id) rather than inserting a second one.
//
// External test package (kindle_test) for the same reason as reconcile_test.go —
// the testutil harness would form an import cycle with an in-package DB test — so
// the fake Cloud Reader wire is injected via the exported SetKindleSource seam.
package kindle_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/ingest/kindle"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// fakeCloudReader is an in-memory kindleSource: a fixed cookie jar + a swappable
// library, so SyncUser runs the full ingest with no network. It structurally
// implements the unexported kindleSource interface, so SetKindleSource accepts
// it (same trick as mapSidecar/positionSource in reconcile_test.go).
type fakeCloudReader struct {
	library  []amazon.CloudLibraryItem
	insights *amazon.KindleInsights
}

func (f *fakeCloudReader) ExchangeWebsiteCookies(context.Context, *amazon.DeviceCredential) (map[string]string, error) {
	return map[string]string{"at-main": "token"}, nil
}
func (f *fakeCloudReader) KindleCloudLibrary(context.Context, map[string]string) ([]amazon.CloudLibraryItem, error) {
	return f.library, nil
}
func (f *fakeCloudReader) FetchKindleInsights(context.Context, map[string]string) (*amazon.KindleInsights, error) {
	if f.insights == nil {
		return &amazon.KindleInsights{}, nil
	}
	return f.insights, nil
}

// newSyncService wires a books Service on the harness DB with a seeded Amazon
// credential (so Amazon.Load succeeds) and the given fake Cloud Reader library.
// Hardcover stays nil — ingest resolves the whole library from Amazon alone, so
// no linkage is needed to prove the write path.
func newSyncService(t *testing.T, hz *testutil.Harness, fk *fakeCloudReader) (*kindle.Service, string) {
	t.Helper()
	// Amazon credential is stored encrypted — install a throwaway
	// BOOM_ENCRYPTION_KEY and reset the memoized AEAD so Save/Load round-trip.
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	t.Setenv("BOOM_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)

	owner, _ := hz.MintUser("kindle_sync")
	az := amazon.NewStore(hz.DB)
	if err := az.Save(context.Background(), owner, amazon.DeviceCredential{CustomerID: "1", DeviceSerial: "dev"}); err != nil {
		t.Fatalf("seed amazon credential: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := kindle.New(hz.DB, az, logger)
	svc.SetKindleSource(fk)
	return svc, owner
}

// kindleRows returns the owner's kindle reading_items keyed by ASIN.
func kindleRows(t *testing.T, d *db.DB, owner string) map[string]db.ReadingItem {
	t.Helper()
	items, err := d.ListReadingItems(context.Background(), owner, "kindle")
	if err != nil {
		t.Fatalf("ListReadingItems: %v", err)
	}
	out := make(map[string]db.ReadingItem, len(items))
	for _, it := range items {
		out[it.ExternalID] = it
	}
	return out
}

// TestSyncUser_WritesReadingItems proves the ingest→reading_items(source='kindle')
// write: SyncUser upserts one row per keyable library item, maps
// percentageRead→status/progress/finished, and drops samples + un-keyable rows.
func TestSyncUser_WritesReadingItems(t *testing.T) {
	hz := testutil.NewHarness(t)
	fk := &fakeCloudReader{library: []amazon.CloudLibraryItem{
		{ASIN: "B0READING1", Title: "In Progress", Authors: []string{"Author, Ada:"}, PercentageRead: 42, CoverURL: "https://img/r.jpg", ResourceType: "EBOOK"},
		{ASIN: "B0FINISHED", Title: "Finished", Authors: []string{"Writer, Bob:"}, PercentageRead: 100, CoverURL: "https://img/d.jpg", ResourceType: "EBOOK"},
		{ASIN: "B0WANT0001", Title: "Unopened", Authors: []string{"Poet, Pat:"}, PercentageRead: 0, ResourceType: "EBOOK"},
		{ASIN: "B0SAMPLE01", Title: "A Sample", PercentageRead: 10, ResourceType: "EBOOK_SAMPLE"}, // filtered
		{ASIN: "", Title: "No ASIN", PercentageRead: 5, ResourceType: "EBOOK"},                    // dropped
	}}
	svc, owner := newSyncService(t, hz, fk)
	cleanupBookData(t, hz.DB, owner)

	n, err := svc.SyncUser(context.Background(), owner)
	if err != nil {
		t.Fatalf("SyncUser: %v", err)
	}
	if n != 3 {
		t.Fatalf("upserted count: want 3 (sample + empty-ASIN dropped), got %d", n)
	}

	rows := kindleRows(t, hz.DB, owner)
	if len(rows) != 3 {
		t.Fatalf("reading_items(kindle) rows: want 3, got %d", len(rows))
	}
	if _, leaked := rows["B0SAMPLE01"]; leaked {
		t.Error("a Kindle sample leaked into reading_items")
	}

	// percentageRead → status/progress/finished, and every row is source='kindle'
	// with amazon_asin set (the fusion/match key).
	if r := rows["B0READING1"]; r.Source != "kindle" || r.Status != "reading" || r.Finished || r.ProgressPercent != 42 || r.AmazonASIN != "B0READING1" || r.Title != "In Progress" {
		t.Errorf("reading row mapped wrong: %+v", r)
	}
	if r := rows["B0FINISHED"]; r.Status != "read" || !r.Finished || r.ProgressPercent != 100 {
		t.Errorf("finished row mapped wrong: %+v", r)
	}
	if r := rows["B0WANT0001"]; r.Status != "want" || r.Finished || r.ProgressPercent != 0 {
		t.Errorf("want row mapped wrong: %+v", r)
	}
}

// TestSyncUser_Idempotent pins the re-run contract: syncing the SAME library
// twice leaves exactly the same rows (no duplicates — the upsert key is
// owner+source+external_id), and a library whose percentageRead advanced UPDATES
// the existing row in place rather than inserting a second one. This is the
// "re-run = no dupes" guarantee the ingest must hold.
func TestSyncUser_Idempotent(t *testing.T) {
	hz := testutil.NewHarness(t)
	fk := &fakeCloudReader{library: []amazon.CloudLibraryItem{
		{ASIN: "B0IDEMP001", Title: "Steady", Authors: []string{"Author, Ada:"}, PercentageRead: 20, ResourceType: "EBOOK"},
		{ASIN: "B0IDEMP002", Title: "Advances", Authors: []string{"Writer, Bob:"}, PercentageRead: 30, ResourceType: "EBOOK"},
	}}
	svc, owner := newSyncService(t, hz, fk)
	cleanupBookData(t, hz.DB, owner)

	ctx := context.Background()
	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("first SyncUser: %v", err)
	}
	first, err := hz.DB.CountReadingItems(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("count after first: %v", err)
	}
	if first != 2 {
		t.Fatalf("after first sync: want 2 rows, got %d", first)
	}

	// One book advanced 30→75 between syncs; the library is otherwise identical.
	fk.library[1].PercentageRead = 75

	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("second SyncUser: %v", err)
	}
	second, err := hz.DB.CountReadingItems(ctx, owner, "kindle")
	if err != nil {
		t.Fatalf("count after second: %v", err)
	}
	if second != 2 {
		t.Fatalf("re-run created duplicates: want 2 rows, got %d", second)
	}

	rows := kindleRows(t, hz.DB, owner)
	// The advanced book updated in place (still one row, new progress/status).
	if r := rows["B0IDEMP002"]; r.ProgressPercent != 75 || r.Status != "reading" {
		t.Errorf("advanced book not updated in place: %+v", r)
	}
	// The steady book is unchanged.
	if r := rows["B0IDEMP001"]; r.ProgressPercent != 20 || r.Status != "reading" {
		t.Errorf("steady book drifted: %+v", r)
	}
}
