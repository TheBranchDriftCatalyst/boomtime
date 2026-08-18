// reconcile_test.go — end-to-end (DB-backed) test of ReconcileKindleStatus, the
// honest-status sweep. A per-ASIN fake sidecar returns an lpr for some books and
// a 404 for others; the sweep must mark the lpr books 'reading' + seed a position
// sample, leave the 404 books 'want', and NEVER touch a read/finished row. It
// also pins idempotency (a re-run is a no-op) and ctx-cancellation. External test
// package (books_test) for the same reason as reading_time_poll_test.go — the
// testutil harness would form an import cycle with an in-package DB test.
package kindle_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/ingest/kindle"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// sidecarOutcome scripts one ASIN's sidecar response.
type sidecarOutcome struct {
	pos int64
	at  time.Time
	ok  bool // true = an lpr exists (200); false = clean miss (404)
	err error
}

// mapSidecar is a per-ASIN positionSource: an ASIN in `out` returns its scripted
// outcome; any other ASIN is a clean miss (ok=false), the sidecar's 404 default.
// It structurally implements the unexported positionSource interface, so
// SetSidecar accepts it. cancelAt/cancel let a test cancel the sweep mid-loop.
type mapSidecar struct {
	out      map[string]sidecarOutcome
	calls    int
	cancelAt int                // fire cancel on the Nth call (0 = never)
	cancel   context.CancelFunc // set by the ctx-cancel test
}

func (m *mapSidecar) FetchLastPagePosition(_ context.Context, _ *amazon.DeviceCredential, asin string) (int64, time.Time, bool, error) {
	m.calls++
	if m.cancel != nil && m.calls == m.cancelAt {
		m.cancel()
	}
	o := m.out[asin] // zero value = {ok:false} = clean miss
	return o.pos, o.at, o.ok, o.err
}

// newReconcileService wires a books Service on the harness DB with a seeded
// Amazon credential (so Amazon.Load succeeds) and the given per-ASIN sidecar.
func newReconcileService(t *testing.T, hz *testutil.Harness, sc *mapSidecar) (*kindle.Service, string) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("gen key: %v", err)
	}
	t.Setenv("BOOM_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)

	owner, _ := hz.MintUser("kindle_reconcile")
	az := amazon.NewStore(hz.DB)
	if err := az.Save(context.Background(), owner, amazon.DeviceCredential{CustomerID: "1", DeviceSerial: "dev"}); err != nil {
		t.Fatalf("seed amazon credential: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := kindle.New(hz.DB, az, logger)
	svc.SetSidecar(sc)
	return svc, owner
}

// upsertKindle seeds one kindle reading_item with an explicit status.
func upsertKindle(t *testing.T, d *db.DB, owner, asin, status string, finished bool) {
	t.Helper()
	it := db.ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: asin, AmazonASIN: asin,
		Title: "Book " + asin, Status: status, Finished: finished,
	}
	if finished {
		fa := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
		it.FinishedAt = &fa
	}
	if err := d.UpsertReadingItem(context.Background(), it); err != nil {
		t.Fatalf("seed kindle row %s: %v", asin, err)
	}
}

// statusOf reads one kindle row's current status.
func statusOf(t *testing.T, d *db.DB, owner, asin string) (string, bool) {
	t.Helper()
	items, err := d.ListReadingItems(context.Background(), owner, "kindle")
	if err != nil {
		t.Fatalf("ListReadingItems: %v", err)
	}
	for _, it := range items {
		if it.ExternalID == asin {
			return it.Status, it.Finished
		}
	}
	t.Fatalf("kindle row %s not found", asin)
	return "", false
}

func TestReconcileKindleStatus_MarksReadingSeedsAndLeavesWants(t *testing.T) {
	hz := testutil.NewHarness(t)

	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	sc := &mapSidecar{out: map[string]sidecarOutcome{
		// Two opened books (an lpr exists) → should become 'reading' + seeded.
		"B0OK0001": {pos: 512, at: at, ok: true},
		"B0OK0002": {pos: 900, at: at, ok: true},
		// A read/finished book with an end-of-book lpr — the sweep must never even
		// consider it (filtered as a candidate) and never demote it.
		"B0READ01": {pos: 9999, at: at, ok: true},
		// B0MISS01 intentionally absent → clean miss (404) → stays 'want'.
	}}
	svc, owner := newReconcileService(t, hz, sc)
	cleanupBookData(t, hz.DB, owner)

	upsertKindle(t, hz.DB, owner, "B0OK0001", "want", false)
	upsertKindle(t, hz.DB, owner, "B0OK0002", "want", false)
	upsertKindle(t, hz.DB, owner, "B0MISS01", "want", false)
	upsertKindle(t, hz.DB, owner, "B0READ01", "read", true)

	ctx := context.Background()
	res, err := svc.ReconcileKindleStatus(ctx, owner)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// Read/finished row is excluded from candidates → only the 3 non-read rows.
	if res.Scanned != 3 {
		t.Errorf("Scanned = %d, want 3 (read row excluded)", res.Scanned)
	}
	if res.MarkedReading != 2 {
		t.Errorf("MarkedReading = %d, want 2", res.MarkedReading)
	}
	if res.StillWant != 1 {
		t.Errorf("StillWant = %d, want 1 (the 404 book)", res.StillWant)
	}
	if res.Seeded != 2 {
		t.Errorf("Seeded = %d, want 2 (one sample per lpr book)", res.Seeded)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0", res.Errors)
	}

	// Statuses: the two lpr books are 'reading'; the miss stays 'want'; the read
	// book is untouched.
	if s, _ := statusOf(t, hz.DB, owner, "B0OK0001"); s != "reading" {
		t.Errorf("B0OK0001 status = %q, want reading", s)
	}
	if s, _ := statusOf(t, hz.DB, owner, "B0OK0002"); s != "reading" {
		t.Errorf("B0OK0002 status = %q, want reading", s)
	}
	if s, _ := statusOf(t, hz.DB, owner, "B0MISS01"); s != "want" {
		t.Errorf("B0MISS01 status = %q, want want", s)
	}
	if s, fin := statusOf(t, hz.DB, owner, "B0READ01"); s != "read" || !fin {
		t.Errorf("B0READ01 = (%q, finished=%v), want (read, true) — must never be demoted", s, fin)
	}

	// Position samples seeded ONLY for the two lpr books (dedup index means one row
	// each); none for the miss or the read book.
	for _, tc := range []struct {
		asin string
		want int
	}{
		{"B0OK0001", 1}, {"B0OK0002", 1}, {"B0MISS01", 0}, {"B0READ01", 0},
	} {
		rows, lerr := hz.DB.ListKindleReadingPositions(ctx, owner, tc.asin, time.Time{})
		if lerr != nil {
			t.Fatalf("list positions %s: %v", tc.asin, lerr)
		}
		if len(rows) != tc.want {
			t.Errorf("%s: %d position samples, want %d", tc.asin, len(rows), tc.want)
		}
	}

	// Idempotent: a second sweep re-polls, flips nothing new (already 'reading'),
	// and seeds nothing new (the lpr's creationTime dedupes on the unique index).
	res2, err := svc.ReconcileKindleStatus(ctx, owner)
	if err != nil {
		t.Fatalf("reconcile (re-run): %v", err)
	}
	if res2.MarkedReading != 0 {
		t.Errorf("re-run MarkedReading = %d, want 0 (already reading)", res2.MarkedReading)
	}
	if res2.Seeded != 0 {
		t.Errorf("re-run Seeded = %d, want 0 (samples dedupe)", res2.Seeded)
	}
	if res2.StillWant != 1 {
		t.Errorf("re-run StillWant = %d, want 1", res2.StillWant)
	}
	// Statuses unchanged after the idempotent re-run.
	if s, _ := statusOf(t, hz.DB, owner, "B0OK0001"); s != "reading" {
		t.Errorf("after re-run B0OK0001 = %q, want reading", s)
	}
	if s, _ := statusOf(t, hz.DB, owner, "B0MISS01"); s != "want" {
		t.Errorf("after re-run B0MISS01 = %q, want want", s)
	}
}

func TestReconcileKindleStatus_ContextCancel(t *testing.T) {
	hz := testutil.NewHarness(t)

	ctx, cancel := context.WithCancel(context.Background())
	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	// Cancel during the FIRST book's sidecar call — the loop must stop before
	// scanning the rest (it re-checks ctx.Err() at the top of each iteration).
	sc := &mapSidecar{
		out: map[string]sidecarOutcome{
			"B0CAN0001": {pos: 1, at: at, ok: true},
			"B0CAN0002": {pos: 2, at: at, ok: true},
			"B0CAN0003": {pos: 3, at: at, ok: true},
		},
		cancelAt: 1,
		cancel:   cancel,
	}
	svc, owner := newReconcileService(t, hz, sc)
	cleanupBookData(t, hz.DB, owner)

	upsertKindle(t, hz.DB, owner, "B0CAN0001", "want", false)
	upsertKindle(t, hz.DB, owner, "B0CAN0002", "want", false)
	upsertKindle(t, hz.DB, owner, "B0CAN0003", "want", false)

	res, err := svc.ReconcileKindleStatus(ctx, owner)
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The sweep stopped early: only the first book was scanned, not all three.
	if res.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1 (cancelled after the first book)", res.Scanned)
	}
	if sc.calls != 1 {
		t.Errorf("sidecar calls = %d, want 1 (sweep stopped)", sc.calls)
	}
}
