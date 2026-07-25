// worker_test.go — end-to-end coverage of the labelimages worker against an
// httptest shim + a live isolated Postgres. Non-tautological: verifies that
// a Run cycle actually inserts rows AND that a second Run skips the same
// ids (using a call counter on the httptest server).
package labelimages

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"
)

const testDatabaseURL = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"

// openTestDB gives us a live DB or Skips when Postgres is unreachable — same
// convention as internal/db test helpers.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		// caller override honored
	}
	url := testDatabaseURL
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		url = v
	}
	ctx := context.Background()
	d, err := db.New(ctx, url)
	if err != nil {
		t.Skipf("labelimages worker test: no test DB (%s): %v", url, err)
	}
	return d
}

// pngBytes: PNG magic + payload so the DB save + mime sniff works.
func pngBytes(tag string) []byte {
	return append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte(tag)...)
}

// shimServer starts a fake shim that returns unique bytes per prompt so
// tests can distinguish saved-rows-per-label.
func shimServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if r.URL.Path != "/v1/images/generations" {
			http.Error(w, "unexpected path", 404)
			return
		}
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		prompt, _ := req["prompt"].(string)
		// One prompt -> one deterministic byte string.
		bytes := pngBytes(prompt)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(bytes)}},
		})
	}))
}

// fixtureEntries: 2 labels for the worker to iterate.
func fixtureEntries() []labelcatalog.Entry {
	return []labelcatalog.Entry{
		{ID: "test-w-a", Prompt: "prompt A"},
		{ID: "test-w-b", Prompt: "prompt B"},
	}
}

func cleanupTestRows(t *testing.T, d *db.DB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		_ = d.DeleteLabelImage(context.Background(), id)
	}
}

// TestWorker_Run_GeneratesMissing: first Run of a clean slate hits the shim
// once per label; every label ends up with a row whose bytes match the
// prompt-derived shim response.
func TestWorker_Run_GeneratesMissing(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	cleanupTestRows(t, d, "test-w-a", "test-w-b")
	defer cleanupTestRows(t, d, "test-w-a", "test-w-b")

	var hits atomic.Int32
	srv := shimServer(t, &hits)
	defer srv.Close()

	client, err := comfyui.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newWorkerForTest(d, client, "test-model", logger, fixtureEntries())

	w.Run(context.Background())

	if got := hits.Load(); got != 2 {
		t.Errorf("shim hits=%d want 2 (one per label)", got)
	}

	got, ok, err := d.GetLabelImage(context.Background(), "test-w-a")
	if err != nil || !ok {
		t.Fatalf("row for test-w-a: ok=%v err=%v", ok, err)
	}
	if string(got.ImageBytes) != string(pngBytes("prompt A")) {
		t.Errorf("test-w-a bytes wrong: got %q", string(got.ImageBytes))
	}
	if got.Model != "test-model" || got.Prompt != "prompt A" {
		t.Errorf("provenance not saved: model=%q prompt=%q", got.Model, got.Prompt)
	}
}

// TestWorker_Run_SkipsExisting: a second Run should NOT re-hit the shim for
// labels that already have a row. Non-tautological: the shim call counter
// stays at N after the second Run (where N is the count from the first).
func TestWorker_Run_SkipsExisting(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	cleanupTestRows(t, d, "test-w-a", "test-w-b")
	defer cleanupTestRows(t, d, "test-w-a", "test-w-b")

	var hits atomic.Int32
	srv := shimServer(t, &hits)
	defer srv.Close()

	client, _ := comfyui.NewClient(srv.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newWorkerForTest(d, client, "test-model", logger, fixtureEntries())

	w.Run(context.Background())
	first := hits.Load()
	w.Run(context.Background()) // second run — should be a no-op
	second := hits.Load()

	if second != first {
		t.Errorf("shim was called on second run: first=%d second=%d (worker should skip existing rows)", first, second)
	}
}

// TestWorker_RegenerateAll_TruncatesAndReplaces: a --all regeneration
// deletes existing rows and re-hits the shim for every label.
func TestWorker_RegenerateAll_TruncatesAndReplaces(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	cleanupTestRows(t, d, "test-w-a", "test-w-b")
	defer cleanupTestRows(t, d, "test-w-a", "test-w-b")

	var hits atomic.Int32
	srv := shimServer(t, &hits)
	defer srv.Close()

	client, _ := comfyui.NewClient(srv.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newWorkerForTest(d, client, "model-v1", logger, fixtureEntries())

	// Seed both labels with a stub row so we can prove the Truncate wiped them.
	if err := d.SaveLabelImage(context.Background(), "test-w-a", pngBytes("OLD-A"), "image/png", "old-model", "old prompt", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveLabelImage(context.Background(), "test-w-b", pngBytes("OLD-B"), "image/png", "old-model", "old prompt", nil); err != nil {
		t.Fatal(err)
	}

	// TruncateLabelImages inside RegenerateAll should delete these AND
	// every other row currently in the table — but this test only cares
	// about the two we seeded. Note: this test shares the DB with the
	// startup worker (if a real one runs elsewhere). Guard with an
	// explicit assertion on OUR rows only.
	gen, failed, err := w.RegenerateAll(context.Background())
	if err != nil {
		t.Fatalf("RegenerateAll: %v", err)
	}
	if failed != 0 {
		t.Errorf("failed=%d want 0", failed)
	}
	if gen != 2 {
		t.Errorf("generated=%d want 2", gen)
	}

	// The row must now carry the NEW model + NEW bytes.
	got, _, _ := d.GetLabelImage(context.Background(), "test-w-a")
	if got == nil || got.Model != "model-v1" {
		t.Errorf("row after regenerate-all didn't carry new model: %+v", got)
	}
	if string(got.ImageBytes) == string(pngBytes("OLD-A")) {
		t.Errorf("row bytes still the OLD seed — Truncate didn't wipe or worker didn't re-generate")
	}
}

// TestWorker_RegenerateOne_UnknownID: an unknown id returns an error before
// touching the DB.
func TestWorker_RegenerateOne_UnknownID(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	srv := shimServer(t, nil)
	defer srv.Close()
	client, _ := comfyui.NewClient(srv.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := newWorkerForTest(d, client, "test-model", logger, fixtureEntries())

	err := w.RegenerateOne(context.Background(), "does-not-exist-in-catalog")
	if err == nil {
		t.Fatal("expected error for unknown id, got nil")
	}
}

// TestWorker_NilReceiver_NoOp: a nil worker (feature disabled) is a graceful
// no-op for Run and returns errors for the CLI methods (so an operator
// invoking `boomtime label-images regenerate` without the flag gets a clear
// error, not a silent no-op).
func TestWorker_NilReceiver_NoOp(t *testing.T) {
	var w *Worker
	// Run must not panic.
	w.Run(context.Background())
	// CLI methods must return an error, not silently succeed.
	if err := w.RegenerateOne(context.Background(), "late-night-coder"); err == nil {
		t.Error("nil-worker RegenerateOne: expected error, got nil")
	}
	if _, _, err := w.RegenerateAll(context.Background()); err == nil {
		t.Error("nil-worker RegenerateAll: expected error, got nil")
	}
}
