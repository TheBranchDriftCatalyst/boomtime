// import_test.go: verifies the gaka-6jm.8 handler-side change — the
// ImportRequest endpoint MUST NOT eagerly persist a typed Wakatime key on
// submit. Persistence is deferred to the worker's terminal-success path
// (see importer.applyKeyOutcome + its own tests in the importer package).
//
// This is the handler-level guarantee. If someone reintroduces the eager
// save in import.go this test flips red immediately without needing a full
// worker-run integration.
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// TestImportRequestDoesNotEagerlySaveTypedKey posts an import request with a
// typed apiToken and asserts that immediately after the handler returns, the
// row's encrypted_wakatime_key is still NULL. Save happens later, only if
// the worker's import completes without a 401.
func TestImportRequestDoesNotEagerlySaveTypedKey(t *testing.T) {
	hz := testutil.NewHarness(t)
	user, token := hz.MintUser("import_noneager")

	// Build our own Handler with a REAL Worker (the default harness passes
	// nil for the worker, and ImportRequest calls Worker.StartJob synchronously
	// before returning, so a nil worker panics). We use a background context
	// bound to the test so the worker goroutine's DB writes fail cleanly
	// under cleanup rather than leak.
	workerCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	silent := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := importer.NewHub()
	worker := importer.NewWorker(workerCtx, hz.DB, silent, hub)
	cfg := &config.Config{Port: 8080, EnableRegistration: true, SessionExpiry: 24}
	h := handler.New(hz.DB, cfg, silent, worker, hub, nil)

	e := echo.New()
	e.POST("/auth/login", h.Login) // route table shim so echo doesn't 404 on unrelated middleware
	e.POST("/api/v1/users/current/import", h.ImportRequest)

	// Confirm baseline: no saved key.
	if _, has, err := hz.DB.GetEncryptedWakatimeKey(context.Background(), user); err != nil || has {
		t.Fatalf("baseline: expected no saved key, got has=%v err=%v", has, err)
	}

	// Build a well-formed import payload with a typed key. The dates are
	// close together so the worker (if it ever starts) does minimal work,
	// but the handler returns the queued job before any wakatime.com round
	// trip anyway.
	now := time.Now().UTC()
	body := map[string]any{
		"apiToken":  "waka_never_persist_me_eagerly",
		"startDate": now.Format(time.RFC3339),
		"endDate":   now.Format(time.RFC3339),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/import", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("import submit: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// The load-bearing assertion: the handler must NOT have written the
	// ciphertext eagerly. The worker may write it later on success; that path
	// is covered by importer/apply_key_outcome_test.go.
	if _, has, err := hz.DB.GetEncryptedWakatimeKey(context.Background(), user); err != nil {
		t.Fatalf("read back: %v", err)
	} else if has {
		t.Fatalf("gaka-6jm.8 violated: handler eagerly persisted the typed key on submit")
	}

	// Best-effort: cancel any running job before the DB gets torn down so the
	// worker's terminal DB write doesn't race the test cleanup.
	if _, err := hz.DB.MarkRunningJobsFailed(context.Background(), "import_test cleanup"); err != nil {
		t.Logf("cleanup marking jobs failed: %v", err)
	}
}
