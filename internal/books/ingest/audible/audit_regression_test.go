// audit_regression_test.go — regression pins for the audible-side findings of the
// 2026-09-06 audit (epic boom-l827):
//
//   - audiobooks.go:636 — the forward library sweep is cursored on purchase date, so
//     progress for already-owned titles froze at backfill time.
//   - audiobooks.go:638 — any sweep error (a DNS blip, a deploy cancelling the job)
//     flipped the Amazon device credential to "invalid".
//   - audiobooks.go:928 — the in-progress push read the DERIVED status and ignored the
//     curation override, so a book curated to dnf/paused was still pushed as "reading".
//   - audiobooks.go:815 — the finish push never reused or recorded hardcover_read_id
//     and never stamped the echo-suppression columns.
//
// The DB-backed cases connect directly to the isolated harness DB (the same reason
// match_sweep_test.go does: internal/shared/testutil transitively imports this tree).
package audible

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

const audibleTestDefaultDSN = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"

var (
	audibleDBOnce sync.Once
	audibleDB     *db.DB
	audibleDBErr  error
)

func openAudibleDB(t *testing.T) *db.DB {
	t.Helper()
	dsn := os.Getenv("BOOM_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = audibleTestDefaultDSN
	}
	audibleDBOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := db.MigrateURL(ctx, dsn); err != nil {
			audibleDBErr = fmt.Errorf("migrate: %w", err)
			return
		}
		audibleDB, audibleDBErr = db.New(ctx, dsn)
	})
	if audibleDBErr != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("test DB required but unavailable: %v", audibleDBErr)
		}
		t.Skipf("skipping: isolated test DB unavailable: %v", audibleDBErr)
	}
	return audibleDB
}

func seedAudibleOwner(t *testing.T, d *db.DB, ctx context.Context, owner string) {
	t.Helper()
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM reading_items WHERE owner=$1`, owner)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, owner)
	})
}

// TestDueForFullLibrarySweep pins the cadence that un-freezes progress for
// already-purchased audiobooks. The naive "last sweep older than 24h" test cannot
// work: last_forward_at is rewritten by EVERY forward run (hourly in prod), so it
// never ages past a duration threshold — hence the UTC calendar-day rule.
func TestDueForFullLibrarySweep(t *testing.T) {
	now := time.Date(2026, 9, 6, 3, 15, 0, 0, time.UTC)
	at := func(h int, dayOffset int) *time.Time {
		v := now.AddDate(0, 0, dayOffset)
		v = time.Date(v.Year(), v.Month(), v.Day(), h, 0, 0, 0, time.UTC)
		return &v
	}
	cases := []struct {
		name string
		last *time.Time
		want bool
	}{
		{"never run before", nil, true},
		{"an hour ago, same UTC day", at(2, 0), false},
		{"earlier today", at(0, 0), false},
		{"late yesterday", at(23, -1), true},
		{"last week", at(12, -7), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dueForFullLibrarySweep(c.last, now); got != c.want {
				t.Fatalf("dueForFullLibrarySweep = %v, want %v", got, c.want)
			}
		})
	}

	// The hourly cadence must produce exactly ONE full sweep per UTC day, not one
	// per run and not none.
	day := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 9, 5, 23, 30, 0, 0, time.UTC)
	full := 0
	for h := 0; h < 24; h++ {
		runAt := day.Add(time.Duration(h) * time.Hour)
		if dueForFullLibrarySweep(&last, runAt) {
			full++
		}
		last = runAt
	}
	if full != 1 {
		t.Fatalf("24 hourly forward runs did %d full sweeps, want exactly 1", full)
	}
}

// TestInProgressPush_HonoursCurationOverride pins audiobooks.go:928: the push
// selector must read the EFFECTIVE status. A book the user curated to dnf/paused was
// still pushed as "reading" the next time its percent moved, flipping their Hardcover
// shelf entry out of DNF and back into Currently Reading.
func TestInProgressPush_HonoursCurationOverride(t *testing.T) {
	sp := func(s string) *string { return &s }
	cases := []struct {
		name   string
		item   db.ReadingItem
		wantOK bool
	}{
		{"derived reading, no override", db.ReadingItem{Status: "reading", ProgressPercent: 40}, true},
		{"derived reading, curated to dnf", db.ReadingItem{Status: "reading", ProgressPercent: 40, StatusOverride: sp("dnf")}, false},
		{"derived reading, curated to paused", db.ReadingItem{Status: "reading", ProgressPercent: 40, StatusOverride: sp("paused")}, false},
		{"derived reading, curated to read", db.ReadingItem{Status: "reading", ProgressPercent: 40, StatusOverride: sp("read")}, false},
		{"derived want, curated to reading", db.ReadingItem{Status: "want", ProgressPercent: 40, StatusOverride: sp("reading")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, ok := inProgressPush(c.item)
			if ok != c.wantOK {
				t.Fatalf("inProgressPush ok = %v, want %v (effective status %q)",
					ok, c.wantOK, c.item.EffectiveStatus())
			}
		})
	}
}

// TestRecordFinishPush_CachesReadIDAndStampsPush pins audiobooks.go:815 on real SQL:
// after a finish push lands, the read id must be cached (so the next push updates
// that read instead of inserting a duplicate) and the echo-suppression columns must
// be stamped (so the next pull does not adopt our own write into the override layer).
func TestRecordFinishPush_CachesReadIDAndStampsPush(t *testing.T) {
	d := openAudibleDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("aud_finishpush_%d", time.Now().UnixNano())
	const asin = "B0AUDFIN01"
	seedAudibleOwner(t, d, ctx, owner)

	if err := d.UpsertReadingItem(ctx, db.ReadingItem{
		Owner: owner, Source: source, ExternalID: asin, AmazonASIN: asin,
		Title: "Endurance", Authors: "Alfred Lansing", Status: "read", Finished: true,
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	svc := New(d, nil, nil)

	// First push: nothing cached yet, so the client inserted a NEW read (id 7001).
	if got := svc.cachedReadID(ctx, owner, asin); got != 0 {
		t.Fatalf("cachedReadID on a never-pushed row = %d, want 0", got)
	}
	svc.recordFinishPush(ctx, owner, asin, 7001, 0)

	it, err := d.GetReadingItem(ctx, owner, source, asin)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.HardcoverReadID == nil || *it.HardcoverReadID != 7001 {
		t.Fatalf("finish push did not cache the read id (%v) — a later curation push "+
			"inserts a SECOND user_book_read for the same finish", it.HardcoverReadID)
	}
	if it.HardcoverStatus == nil || *it.HardcoverStatus != "read" {
		t.Fatalf("finish push did not mirror the remote shelf status: %v", it.HardcoverStatus)
	}

	// Second push for the same book now reuses that id, so UpsertRead UPDATES.
	if got := svc.cachedReadID(ctx, owner, asin); got != 7001 {
		t.Fatalf("cachedReadID = %d, want the cached 7001", got)
	}

	// The echo-suppression stamp landed: a pull echoing our own 'read' right back
	// must NOT be adopted into the sticky override layer.
	if _, err := d.Pool.Exec(ctx,
		`UPDATE reading_items SET hardcover_book_id=990001 WHERE owner=$1 AND source=$2 AND external_id=$3`,
		owner, source, asin); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := d.UpdateHardcoverLinkFromPull(ctx, owner, db.HardcoverUserBookLink{
		BookID: 990001, Status: "read", RemoteUpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	it, err = d.GetReadingItem(ctx, owner, source, asin)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if it.StatusOverride != nil {
		t.Fatalf("our own finish push echoed back into the OVERRIDE layer as status_override=%q "+
			"— ingest must never write that layer", *it.StatusOverride)
	}
}

// TestNoteCredentialOutcome pins audiobooks.go:638 end to end on the users row: a
// transport/lifecycle failure leaves the device status exactly as it was, while a
// credential-shaped failure still marks it invalid and a success still marks it valid.
func TestNoteCredentialOutcome(t *testing.T) {
	d := openAudibleDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("aud_credstat_%d", time.Now().UnixNano())
	seedAudibleOwner(t, d, ctx, owner)

	// UpdateAmazonDeviceStatus only writes rows that actually hold a credential.
	if _, err := d.Pool.Exec(ctx,
		`UPDATE users SET encrypted_amazon_device='\x00', amazon_device_status='valid' WHERE username=$1`,
		owner); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	svc := New(d, amazon.NewStore(d), nil)
	statusOf := func() string {
		info, err := d.GetAmazonDeviceInfo(ctx, owner)
		if err != nil {
			t.Fatalf("GetAmazonDeviceInfo: %v", err)
		}
		if info.Status == nil {
			return "<nil>"
		}
		return *info.Status
	}

	// A deploy cancels the job mid-sweep.
	svc.noteCredentialOutcome(ctx, owner, fmt.Errorf("sweep library: %w", context.Canceled))
	if got := statusOf(); got != "valid" {
		t.Fatalf("a cancelled job flipped the device status to %q — the settings UI now tells "+
			"the user to reconnect Amazon over a deploy", got)
	}

	// A DNS blip.
	svc.noteCredentialOutcome(ctx, owner, &net.DNSError{Err: "no such host", Name: "api.audible.com"})
	if got := statusOf(); got != "valid" {
		t.Fatalf("a DNS failure flipped the device status to %q", got)
	}

	// Amazon actually rejected the credential — this must still flip it.
	svc.noteCredentialOutcome(ctx, owner, errors.New("amazon: library returned HTTP 401: unauthorized"))
	if got := statusOf(); got != "invalid" {
		t.Fatalf("a real credential rejection no longer marks the device invalid: %q", got)
	}

	// …and a success restores it.
	svc.noteCredentialOutcome(ctx, owner, nil)
	if got := statusOf(); got != "valid" {
		t.Fatalf("a successful sweep did not mark the device valid: %q", got)
	}
}
