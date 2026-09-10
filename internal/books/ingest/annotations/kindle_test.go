// kindle_test.go — the Kindle annotation sweep, driven by a fake notebook so the
// error posture is testable without a network.
//
// The load-bearing test here is TestSweepEveryBookUnparseableTurnsRed. Everything
// else in this file describes behaviour; that one prevents a silently-emptied
// corpus.
package annotations

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// fakeNotebook drives the sweep with canned pages.
type fakeNotebook struct {
	books     []amazon.NotebookBook
	perBook   map[string][]amazon.KindleAnnotation
	bookErr   map[string]error
	listErr   error
	cookieErr error
	fetched   []string
}

func (f *fakeNotebook) ExchangeWebsiteCookies(context.Context, *amazon.DeviceCredential) (map[string]string, error) {
	if f.cookieErr != nil {
		return nil, f.cookieErr
	}
	return map[string]string{"session-id": "x"}, nil
}

func (f *fakeNotebook) ListAnnotatedBooks(context.Context, map[string]string) ([]amazon.NotebookBook, error) {
	return f.books, f.listErr
}

func (f *fakeNotebook) FetchBookAnnotations(_ context.Context, _ map[string]string, asin string) ([]amazon.KindleAnnotation, error) {
	f.fetched = append(f.fetched, asin)
	if err := f.bookErr[asin]; err != nil {
		return nil, err
	}
	return f.perBook[asin], nil
}

// newSweepFixture wires a Service against a real DB with a fake notebook and a
// throwaway owner. The credential is never used by the fake, but the sweep loads
// one, so a device row is stashed for the owner.
func newSweepFixture(t *testing.T, nb *fakeNotebook) (*Service, string, context.Context) {
	t.Helper()
	kindleAnnotationPace = 0 // no sleeping in tests

	database := testutil.OpenDB(t)
	ctx := context.Background()
	owner := fmt.Sprintf("annsweep_%d", time.Now().UnixNano())
	if _, err := database.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00')`, owner); err != nil {
		t.Fatalf("mint user: %v", err)
	}
	t.Cleanup(func() { _, _ = database.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner) })

	// The sweep loads a real credential before touching the notebook, so one is
	// sealed under a deterministic test key — exercising the same Store.Load path
	// production uses rather than stubbing it out.
	seedAmazonDevice(t, database, owner)

	svc := New(database, amazon.NewStore(database), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetNotebookSource(nb)
	return svc, owner, ctx
}

// seedAmazonDevice seals a throwaway Amazon device credential for owner under a
// deterministic BOOM_ENCRYPTION_KEY.
func seedAmazonDevice(t *testing.T, database *db.DB, owner string) {
	t.Helper()
	t.Setenv("BOOM_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32)))
	if err := auth.LoadKeyFromEnv(); err != nil {
		t.Fatalf("load encryption key: %v", err)
	}
	err := amazon.NewStore(database).Save(context.Background(), owner, amazon.DeviceCredential{
		AdpToken: "t", DevicePrivateKey: "k", RefreshToken: "r",
		Marketplace: amazon.MarketplaceUS, CustomerID: "cid",
	})
	if err != nil {
		t.Fatalf("seed amazon device: %v", err)
	}
}

func highlightAt(pos int64, body string) amazon.KindleAnnotation {
	return amazon.KindleAnnotation{
		AnnotationID: fmt.Sprintf("ANNOT-%d", pos),
		Kind:         amazon.AnnotationKindHighlightStr,
		Body:         body,
		Position:     pos,
		PositionUnit: amazon.PositionUnitLocationStr,
	}
}

// ⚠ THE REGRESSION TEST. Every book unparseable is a broken parser, not an empty
// account. Mutation: drop the `shapeFails == len(books)` check in
// SyncKindleAnnotations → the job goes GREEN having written zero rows, and the
// next reconcile has nothing to compare against.
func TestSweepEveryBookUnparseableTurnsRed(t *testing.T) {
	nb := &fakeNotebook{
		books: []amazon.NotebookBook{{ASIN: "B01"}, {ASIN: "B02"}, {ASIN: "B03"}},
		bookErr: map[string]error{
			"B01": amazon.ErrNotebookShapeUnknown,
			"B02": amazon.ErrNotebookShapeUnknown,
			"B03": amazon.ErrNotebookShapeUnknown,
		},
	}
	svc, owner, ctx := newSweepFixture(t, nb)

	n, err := svc.SyncKindleAnnotations(ctx, owner)
	if err == nil {
		t.Fatalf("a sweep that understood none of %d books must fail, got n=%d err=nil", len(nb.books), n)
	}
	if !errors.Is(err, amazon.ErrNotebookShapeUnknown) {
		t.Fatalf("error should name the shape failure, got %v", err)
	}
}

// ⚠ THE OTHER HALF OF THE SILENT-ZERO GUARD. Every book failing on TRANSPORT —
// expired cookies, a 500, rate limiting — is just as much a failed sweep as a
// moved DOM, and must not report success.
//
// The first version of the guard tested only shapeFails, so this case returned
// (0, nil): a clean run that wrote nothing, with the corpus silently frozen.
// Mutation: narrow the guard back to `shapeFails == len(books)` → red.
func TestSweepEveryBookUnreachableTurnsRed(t *testing.T) {
	boom := errors.New("HTTP 500 from the notebook")
	nb := &fakeNotebook{
		books: []amazon.NotebookBook{{ASIN: "B01"}, {ASIN: "B02"}},
		bookErr: map[string]error{
			"B01": boom,
			"B02": boom,
		},
	}
	svc, owner, ctx := newSweepFixture(t, nb)

	n, err := svc.SyncKindleAnnotations(ctx, owner)
	if err == nil {
		t.Fatalf("a sweep that could not fetch any of %d books must fail, got n=%d err=nil", len(nb.books), n)
	}
	// A transport failure is NOT a parser failure — the message has to send the
	// reader at the credential, not at the DOM.
	if errors.Is(err, amazon.ErrNotebookShapeUnknown) {
		t.Fatalf("transport failure misreported as a parse failure: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to fetch") {
		t.Fatalf("error should name the fetch failure plainly, got: %v", err)
	}
}

// A MIX of both failure kinds, with nothing succeeding, still has to go red —
// this is the case a guard written as `shapeFails == len(books)` OR
// `otherFails == len(books)` would let through.
func TestSweepMixedTotalFailureTurnsRed(t *testing.T) {
	nb := &fakeNotebook{
		books: []amazon.NotebookBook{{ASIN: "B01"}, {ASIN: "B02"}},
		bookErr: map[string]error{
			"B01": amazon.ErrNotebookShapeUnknown,
			"B02": errors.New("HTTP 500"),
		},
	}
	svc, owner, ctx := newSweepFixture(t, nb)

	_, err := svc.SyncKindleAnnotations(ctx, owner)
	if err == nil {
		t.Fatal("no book succeeded, so the sweep must fail even with mixed causes")
	}
	if !strings.Contains(err.Error(), "1 unparseable") || !strings.Contains(err.Error(), "1 unreachable") {
		t.Fatalf("a mixed failure should account for both causes, got: %v", err)
	}
}

// One moved page must not strand the rest of the sweep.
func TestSweepOneBadBookDoesNotStrandTheOthers(t *testing.T) {
	nb := &fakeNotebook{
		books:   []amazon.NotebookBook{{ASIN: "B01"}, {ASIN: "B02"}, {ASIN: "B03"}},
		bookErr: map[string]error{"B02": amazon.ErrNotebookShapeUnknown},
		perBook: map[string][]amazon.KindleAnnotation{
			"B01": {highlightAt(100, "lorem"), highlightAt(200, "ipsum")},
			"B03": {highlightAt(300, "dolor")},
		},
	}
	svc, owner, ctx := newSweepFixture(t, nb)

	n, err := svc.SyncKindleAnnotations(ctx, owner)
	if err != nil {
		t.Fatalf("one bad book must not fail the sweep: %v", err)
	}
	if n != 3 {
		t.Fatalf("synced %d annotations, want 3 from the two healthy books", n)
	}
	if len(nb.fetched) != 3 {
		t.Fatalf("fetched %v — the sweep should have tried every book", nb.fetched)
	}
}

// Re-running is a no-op, and a genuinely removed highlight is retired — but only
// because the fetch that reported it was itself healthy.
func TestSweepIsIdempotentAndReconcilesRemovals(t *testing.T) {
	nb := &fakeNotebook{
		books: []amazon.NotebookBook{{ASIN: "B01"}},
		perBook: map[string][]amazon.KindleAnnotation{
			"B01": {highlightAt(100, "lorem"), highlightAt(200, "ipsum"), highlightAt(300, "dolor")},
		},
	}
	svc, owner, ctx := newSweepFixture(t, nb)

	for i := 0; i < 2; i++ {
		if _, err := svc.SyncKindleAnnotations(ctx, owner); err != nil {
			t.Fatalf("sweep %d: %v", i, err)
		}
	}
	got, err := svc.DB.ListBookAnnotations(ctx, owner, "kindle", "B01")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("two identical sweeps produced %d rows, want 3", len(got))
	}

	// The user deleted one highlight in the Kindle app.
	nb.perBook["B01"] = []amazon.KindleAnnotation{highlightAt(100, "lorem"), highlightAt(300, "dolor")}
	if _, err := svc.SyncKindleAnnotations(ctx, owner); err != nil {
		t.Fatalf("third sweep: %v", err)
	}
	got, _ = svc.DB.ListBookAnnotations(ctx, owner, "kindle", "B01")
	if len(got) != 2 {
		t.Fatalf("after a genuine removal want 2 live rows, got %d", len(got))
	}
}

// A book the index lists but which turns out to have nothing is a clean success,
// and must not retire anything previously stored for it.
func TestSweepEmptyBookRetiresNothing(t *testing.T) {
	nb := &fakeNotebook{
		books:   []amazon.NotebookBook{{ASIN: "B01"}},
		perBook: map[string][]amazon.KindleAnnotation{"B01": {highlightAt(100, "lorem")}},
	}
	svc, owner, ctx := newSweepFixture(t, nb)
	if _, err := svc.SyncKindleAnnotations(ctx, owner); err != nil {
		t.Fatalf("seed sweep: %v", err)
	}

	// Now the page comes back structurally fine but carrying nothing.
	nb.perBook["B01"] = nil
	if _, err := svc.SyncKindleAnnotations(ctx, owner); err != nil {
		t.Fatalf("empty sweep: %v", err)
	}
	got, _ := svc.DB.ListBookAnnotations(ctx, owner, "kindle", "B01")
	if len(got) != 1 {
		t.Fatalf("an empty fetch retired a stored annotation: %d rows left, want 1", len(got))
	}
}

// The cookie exchange refuses non-US credentials. That must surface as a plain
// stage error — never as a credential-health verdict, which would tell the user
// to reconnect a perfectly good Amazon account.
func TestSweepCookieFailureIsJustAnError(t *testing.T) {
	nb := &fakeNotebook{cookieErr: errors.New("cloud reader is US-only")}
	svc, owner, ctx := newSweepFixture(t, nb)

	if _, err := svc.SyncKindleAnnotations(ctx, owner); err == nil {
		t.Fatal("want an error when cookies cannot be exchanged")
	}
	var status string
	if err := svc.DB.Pool.QueryRow(ctx,
		`SELECT COALESCE(amazon_device_status,'') FROM users WHERE username = $1`, owner).Scan(&status); err != nil {
		t.Fatalf("read device status: %v", err)
	}
	if status == "invalid" {
		t.Fatal("the annotations stage marked the Amazon credential invalid — an optional US-only surface must never do that")
	}
}

// ⚠ The key-collapse guard. If position parsing ever fails, every annotation on
// a book would key to the same string and the corpus would silently collapse to
// one row per kind. Mutation: drop the start<=0 branch in AnnotationKey.
func TestAnnotationKeyDoesNotCollapseWhenPositionIsUnknown(t *testing.T) {
	a := AnnotationKey("highlight", 0, nil, "ANNOT-A")
	b := AnnotationKey("highlight", 0, nil, "ANNOT-B")
	if a == b {
		t.Fatalf("two position-less annotations share the key %q — the corpus would collapse to one row", a)
	}
	if a == "" || b == "" {
		t.Fatalf("keys should fall back to the DOM id, got %q / %q", a, b)
	}
	// With no position AND no id there is nothing stable to key on, so the row
	// must be refused rather than stored under a colliding key.
	if got := AnnotationKey("highlight", 0, nil, ""); got != "" {
		t.Fatalf("an unkeyable annotation must produce an empty key, got %q", got)
	}
	// The normal path stays positional.
	if got := AnnotationKey("highlight", 1234, nil, "ANNOT-A"); got != "highlight:1234" {
		t.Fatalf("keyed annotation = %q", got)
	}
	end := int64(2000)
	if got := AnnotationKey("clip", 1000, &end, ""); got != "clip:1000:2000" {
		t.Fatalf("range key = %q", got)
	}
}

func TestBuildFromKindleDefaultsUnitAndDropsUnkeyable(t *testing.T) {
	row, ok, err := buildFromKindle("o", "B01", highlightAt(100, "lorem"), time.Now())
	if err != nil {
		t.Fatalf("a normal highlight must satisfy its own event declaration: %v", err)
	}
	if !ok {
		t.Fatal("a normal highlight should build")
	}
	if row.PositionUnit != db.PositionUnitLocation || row.PositionStart != 100 || row.Body != "lorem" {
		t.Fatalf("row = %+v", row)
	}
	if row.CapturedAt != nil {
		t.Fatal("the notebook exposes no capture time; inventing one would date every highlight to the first scrape")
	}
	if len(row.RawMeta) == 0 {
		t.Fatal("raw_meta snapshot missing — a parser fix could not re-derive this row")
	}

	bad := amazon.KindleAnnotation{Kind: "highlight"} // no position, no id
	if _, ok, _ := buildFromKindle("o", "B01", bad, time.Now()); ok {
		t.Fatal("an unkeyable annotation must be dropped, not stored")
	}
}
