// zero_progress_regression_test.go — the pipeline-level half of boom-o6q5, on a real
// Postgres. The DB-level pin lives in internal/shared/db
// (TestUpsertReadingItemNoProgress_*); this one proves that SyncUser actually ROUTES
// the Cloud Reader's structural percentageRead=0 down the no-progress path, which is
// the wiring the DB test cannot see.
//
// The Cloud Reader library feed reports percentageRead=0 for EVERY book (the reason
// the status-reconcile and the insights finish-date backfill exist at all). Writing
// that through as want/finished=false made the library sync a DESTRUCTIVE writer of
// the derived layer, which in turn re-armed the transition-only finish promotions
// keyed on prev.finished — so each pipeline cycle silently reverted the user's
// status curation back to 'read'.
package kindle_test

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// small local helpers (the package's other test files own the DB/service harness).
func strPtr(s string) *string { return &s }

func nowUTCDay() time.Time {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// TestSyncUser_ZeroPercentDoesNotDowngradeDerivedState pins the two downgrades the
// zero-progress feed used to cause on a re-sync: a reconcile-set 'reading' row
// regressing to 'want', and a finished row's finished flag flipping back to false
// (the flag every transition-only finish promotion keys on).
func TestSyncUser_ZeroPercentDoesNotDowngradeDerivedState(t *testing.T) {
	hz := testutil.NewHarness(t)
	// Both books come back from Amazon at percentageRead=0 — the feed's normal,
	// information-free shape.
	fk := &fakeCloudReader{library: []amazon.CloudLibraryItem{
		{ASIN: "B0ZP000001", Title: "Reconciled", Authors: []string{"Author, Ada:"}, PercentageRead: 0, ResourceType: "EBOOK"},
		{ASIN: "B0ZP000002", Title: "Finished", Authors: []string{"Writer, Bob:"}, PercentageRead: 0, ResourceType: "EBOOK"},
	}}
	svc, owner := newSyncService(t, hz, fk)
	cleanupBookData(t, hz.DB, owner)
	ctx := context.Background()

	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("sync 1: %v", err)
	}

	// The status-reconcile promotes book 1 to 'reading' (an lpr record exists).
	if flipped, err := hz.DB.SetReadingItemReading(ctx, owner, "kindle", "B0ZP000001"); err != nil || !flipped {
		t.Fatalf("reconcile: err=%v flipped=%v", err, flipped)
	}
	// The insights backfill finishes book 2 and promotes status_override='read'.
	if _, found, err := hz.DB.SetReadingItemFinishedFromInsights(ctx, owner, "B0ZP000002", nowUTCDay()); err != nil || !found {
		t.Fatalf("insights: err=%v found=%v", err, found)
	}
	// The user disagrees about book 2 and curates it to 'dnf'.
	if _, err := hz.DB.SetReadingItemCuration(ctx, owner, "kindle", "B0ZP000002", db.ReadingItemCurationPatch{
		Status: strPtr("dnf"), SetStatus: true,
	}); err != nil {
		t.Fatalf("curate: %v", err)
	}

	// A second identical sync — exactly what the scheduled pipeline does every cycle.
	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("sync 2: %v", err)
	}

	rows := kindleRows(t, hz.DB, owner)
	if r := rows["B0ZP000001"]; r.Status != "reading" {
		t.Errorf("zero-percent re-sync regressed the reconcile-set row to %q — the reading-time "+
			"poller and the in-progress push both skip it until the next reconcile, and the "+
			"diverged push can flip the user's Hardcover shelf to 'want'", r.Status)
	}
	if r := rows["B0ZP000002"]; !r.Finished || r.Status != "read" {
		t.Errorf("zero-percent re-sync reset the finished row (status=%q finished=%v) — this is "+
			"what re-arms the transition-only finish promotion", r.Status, r.Finished)
	}
	// And the user's curation survived, because the promotion never re-fired.
	if r := rows["B0ZP000002"]; r.StatusOverride == nil || *r.StatusOverride != "dnf" {
		t.Errorf("user curation clobbered by the sync/insights cycle: status_override = %v, want dnf",
			r.StatusOverride)
	}
}

// TestSyncUser_RealPercentStillWrites is the counterweight: the guard is scoped to
// the information-free zero, so a book Amazon DOES report progress for still moves
// the derived layer normally.
func TestSyncUser_RealPercentStillWrites(t *testing.T) {
	hz := testutil.NewHarness(t)
	fk := &fakeCloudReader{library: []amazon.CloudLibraryItem{
		{ASIN: "B0ZP000003", Title: "Advancing", Authors: []string{"Author, Ada:"}, PercentageRead: 0, ResourceType: "EBOOK"},
	}}
	svc, owner := newSyncService(t, hz, fk)
	cleanupBookData(t, hz.DB, owner)
	ctx := context.Background()

	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	fk.library[0].PercentageRead = 64
	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if r := kindleRows(t, hz.DB, owner)["B0ZP000003"]; r.Status != "reading" || r.ProgressPercent != 64 {
		t.Fatalf("an informed percent stopped writing the derived layer: %+v", r)
	}

	// …and a real 100% still finishes the row.
	fk.library[0].PercentageRead = 100
	if _, err := svc.SyncUser(ctx, owner); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	if r := kindleRows(t, hz.DB, owner)["B0ZP000003"]; r.Status != "read" || !r.Finished {
		t.Fatalf("100%% no longer finishes the row: %+v", r)
	}
}
