// reading_time_poll_test.go — end-to-end (DB-backed) test of PollReadingTime: it
// stitches sample capture (via a fake sidecar) + the pure composition + the
// reading_activity upsert. External test package (books_test) so it may use the
// testutil harness without the books→testutil→handler→…→books import cycle that
// an in-package (package books) DB test would create.
//
// Two paths are pinned:
//
//   - clean-miss: the sidecar reports no position for the book (ok=false) → NO
//     new samples captured, but the recompose over pre-seeded samples still
//     writes reading_activity(source='kindle'), and a re-run is idempotent (same
//     seconds, one bucket per day).
//   - capturing: a fake that returns a position → a sample is appended.
package reading_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/reading"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// fakeSidecar is an in-memory positionSource. It returns a fixed outcome for
// every ASIN so PollReadingTime exercises without a network — and independently
// of the pending real wire shape. It structurally implements the (unexported)
// positionSource interface, so SetSidecar accepts it.
type fakeSidecar struct {
	pos   int64
	at    time.Time
	ok    bool
	err   error
	calls int
}

func (f *fakeSidecar) FetchLastPagePosition(_ context.Context, _ *amazon.DeviceCredential, _ string) (int64, time.Time, bool, error) {
	f.calls++
	return f.pos, f.at, f.ok, f.err
}

// newPollService wires a books Service on the harness DB with a seeded Amazon
// credential (so Amazon.Load succeeds) and the given fake sidecar. Returns the
// service + owner.
func newPollService(t *testing.T, hz *testutil.Harness, sc *fakeSidecar) (*reading.Service, string) {
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

	owner, _ := hz.MintUser("kindle_poll")
	az := amazon.NewStore(hz.DB)
	if err := az.Save(context.Background(), owner, amazon.DeviceCredential{CustomerID: "1", DeviceSerial: "dev"}); err != nil {
		t.Fatalf("seed amazon credential: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := reading.New(hz.DB, az, logger)
	svc.SetSidecar(sc)
	return svc, owner
}

// seedInProgressBook upserts an in-progress kindle reading_item.
func seedInProgressBook(t *testing.T, d *db.DB, owner, asin string) {
	t.Helper()
	if err := d.UpsertReadingItem(context.Background(), db.ReadingItem{
		Owner: owner, Source: "kindle", ExternalID: asin, AmazonASIN: asin,
		Title: "In Progress " + asin, Status: "reading", ProgressPercent: 40,
	}); err != nil {
		t.Fatalf("seed reading_item: %v", err)
	}
}

func cleanupBookData(t *testing.T, d *db.DB, owner string) {
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = d.DeleteKindleReadingPositions(ctx, owner, "")
		_, _ = d.DeleteReadingActivity(ctx, owner, "")
		_, _ = d.DeleteReadingItems(ctx, owner, "")
	})
}

func TestPollReadingTime_CleanMissRecomposesSeededSamples(t *testing.T) {
	hz := testutil.NewHarness(t)
	// Sidecar reports no position for the book this run (ok=false) — capture is a
	// no-op, but the recompose over existing samples must still run.
	svc, owner := newPollService(t, hz, &fakeSidecar{ok: false})
	cleanupBookData(t, hz.DB, owner)

	asin := "B0POLL01"
	seedInProgressBook(t, hz.DB, owner, asin)

	// Pre-seed two advancing samples 5 min apart on 2026-08-10 → 300s reading.
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	if _, err := hz.DB.InsertKindleReadingPosition(ctx, owner, asin, 100, base); err != nil {
		t.Fatalf("seed sample 1: %v", err)
	}
	if _, err := hz.DB.InsertKindleReadingPosition(ctx, owner, asin, 250, base.Add(5*time.Minute)); err != nil {
		t.Fatalf("seed sample 2: %v", err)
	}

	captured, err := svc.PollReadingTime(ctx, owner)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if captured != 0 {
		t.Errorf("captured = %d, want 0 (shape pending → no new samples)", captured)
	}

	day := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	rows, err := hz.DB.ListReadingActivity(ctx, owner, "kindle", day, day)
	if err != nil {
		t.Fatalf("list activity: %v", err)
	}
	if len(rows) != 1 || rows[0].ListeningSeconds != 300 {
		t.Fatalf("reading_activity = %+v, want one kindle bucket @ 300s (recomposed from samples)", rows)
	}

	// Idempotent: a second poll recomputes the SAME 300s and overwrites — still
	// one bucket, same seconds, no double-count.
	if _, err := svc.PollReadingTime(ctx, owner); err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	rows, _ = hz.DB.ListReadingActivity(ctx, owner, "kindle", day, day)
	if len(rows) != 1 || rows[0].ListeningSeconds != 300 {
		t.Fatalf("after re-poll: %+v, want one kindle bucket @ 300s (idempotent)", rows)
	}
}

func TestPollReadingTime_CapturesSampleWhenSidecarReturnsPosition(t *testing.T) {
	hz := testutil.NewHarness(t)
	at := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	svc, owner := newPollService(t, hz, &fakeSidecar{pos: 512, at: at, ok: true})
	cleanupBookData(t, hz.DB, owner)

	asin := "B0POLL02"
	seedInProgressBook(t, hz.DB, owner, asin)

	ctx := context.Background()
	captured, err := svc.PollReadingTime(ctx, owner)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if captured != 1 {
		t.Fatalf("captured = %d, want 1 (sidecar returned a position)", captured)
	}
	samples, err := hz.DB.ListKindleReadingPositions(ctx, owner, asin, time.Time{})
	if err != nil {
		t.Fatalf("list samples: %v", err)
	}
	if len(samples) != 1 || samples[0].Position != 512 || !samples[0].SampledAt.Equal(at) {
		t.Fatalf("samples = %+v, want one @ pos 512 / %v", samples, at)
	}

	// A single sample can't form an interval, so no reading_activity yet.
	rows, _ := hz.DB.ListReadingActivity(ctx, owner, "kindle", at.Truncate(24*time.Hour), at)
	if len(rows) != 0 {
		t.Errorf("reading_activity = %+v, want none (one sample = no interval)", rows)
	}
}
