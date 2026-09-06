// amazon_credential_error_test.go — DB-backed coverage of the Amazon
// credential precondition shared by the Kindle/Audible backfill, the Kindle
// status reconcile and books sync-all (audit boom-l827).
//
// Every one of those handlers used to do `if _, err := Store.Load(...); err != nil
// { 400 "connect Amazon…" }`, which conflates "no credential" with "the
// credential is there but could not be READ". The second case is real: rotate
// BOOM_ENCRYPTION_KEY without running rotate-encryption-key (or restore a backup
// under a different key) and auth.Decrypt fails for every stored credential —
// while GET /api/v1/amazon keeps answering connected:true off Info(), which
// never decrypts. The result was a "connected" card whose every action said
// "connect Amazon", and the obvious user response — reconnect — DESTROYS the
// ciphertext that is the only evidence of the key mismatch.
//
// The test drives that exact sequence against a real Postgres row: save under
// key A, swap the process key to B, call the endpoint.
package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// stubEnqueuer satisfies jobs.Enqueuer so the handlers get past their
// "background jobs are not available" guard and reach the credential check.
// A reached Enqueue is itself a finding: the credential precondition let a job
// through.
type stubEnqueuer struct{ calls int }

func (s *stubEnqueuer) Enqueue(_ context.Context, _ string, _ []byte, _ ...jobs.EnqueueOption) (int64, error) {
	s.calls++
	return 1, nil
}

// booksCredRouter mounts the four handlers that share the Amazon credential
// precondition, registered through the same apiroute seam production uses (so
// the error→status mapping under test is the real one).
func booksCredRouter(hz *testutil.Harness, enq jobs.Enqueuer) *echo.Echo {
	e := echo.New()
	h := New(hz.DB, &config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetJobEnqueuer(enq)
	apiroute.Accepted(e, http.MethodPost, "/api/v1/kindle/backfill", h.BackfillKindle)
	apiroute.Accepted(e, http.MethodPost, "/api/v1/kindle/reconcile", h.ReconcileKindle)
	apiroute.Accepted(e, http.MethodPost, "/api/v1/audible/backfill", h.BackfillAudible)
	apiroute.Accepted(e, http.MethodPost, "/api/v1/books/sync-all", h.SyncAllBooks)
	return e
}

var credPaths = []string{
	"/api/v1/kindle/backfill",
	"/api/v1/kindle/reconcile",
	"/api/v1/audible/backfill",
	"/api/v1/books/sync-all",
}

func postAs(e *echo.Echo, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set(echo.HeaderAuthorization, "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func newTestEncryptionKey(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

// TestAmazonCredentialPrecondition_UndecryptableCredentialIs500NotConnectAmazon
// is the finding. Nothing about the user's state says "not connected" — the row
// is present and GET /api/v1/amazon would say connected:true — so answering
// "connect Amazon before running a backfill" is a lie that points the user at
// the one action that destroys the evidence.
func TestAmazonCredentialPrecondition_UndecryptableCredentialIs500NotConnectAmazon(t *testing.T) {
	hz := testutil.NewHarness(t) // skips when the isolated test DB is unreachable
	ctx := context.Background()

	// Key A: the key in force when the user connected Amazon.
	t.Setenv(auth.EncryptionKeyEnv, newTestEncryptionKey(t))
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)

	user, token := hz.MintUser("bookscred_stale")
	// Entirely synthetic credential material — no real token or key.
	if err := amazon.NewStore(hz.DB).Save(ctx, user, amazon.DeviceCredential{
		AdpToken:         "{enc:not-a-real-adp-token}",
		DevicePrivateKey: "-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----",
		RefreshToken:     "Atnr|not-a-real-refresh-token",
		Marketplace:      amazon.MarketplaceUS,
		DeviceSerial:     "0000000000000000",
	}); err != nil {
		t.Fatalf("save credential under key A: %v", err)
	}

	// Key B: BOOM_ENCRYPTION_KEY rotated without running rotate-encryption-key.
	// The ciphertext is untouched; it simply no longer authenticates.
	t.Setenv(auth.EncryptionKeyEnv, newTestEncryptionKey(t))
	auth.ResetForTest()

	// Sanity: this really is a decrypt failure, not a missing row.
	if _, lerr := amazon.NewStore(hz.DB).Load(ctx, user); lerr == nil {
		t.Fatal("credential still decrypts after the key swap — the scenario did not set up")
	}

	enq := &stubEnqueuer{}
	e := booksCredRouter(hz, enq)
	for _, path := range credPaths {
		rec := postAs(e, path, token)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want 500 — an undecryptable credential is a server-side "+
				"failure, not a missing connection (body: %s)", path, rec.Code, rec.Body.String())
		}
		var env map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: body is not the JSON error envelope: %s", path, rec.Body.String())
		}
		msg, _ := env["error"].(string)
		if strings.Contains(strings.ToLower(msg), "connect amazon") {
			t.Fatalf("%s: told the user to reconnect Amazon (%q) — reconnecting overwrites the "+
				"ciphertext that proves the key mismatch", path, msg)
		}
		// The generic 500 envelope must not leak the underlying error text.
		if strings.Contains(strings.ToLower(msg), "decrypt") || strings.Contains(msg, user) {
			t.Fatalf("%s: internal error detail leaked to the client: %q", path, msg)
		}
	}
	if enq.calls != 0 {
		t.Fatalf("a job was enqueued (%d) despite an unreadable credential", enq.calls)
	}
}

// TestAmazonCredentialPrecondition_NoCredentialStill400 pins the behaviour the
// fix must NOT change: a user who genuinely never connected Amazon still gets
// the actionable 400, with each endpoint's own wording.
func TestAmazonCredentialPrecondition_NoCredentialStill400(t *testing.T) {
	hz := testutil.NewHarness(t)

	t.Setenv(auth.EncryptionKeyEnv, newTestEncryptionKey(t))
	auth.ResetForTest()
	t.Cleanup(auth.ResetForTest)

	_, token := hz.MintUser("bookscred_none")

	wantMsg := map[string]string{
		"/api/v1/kindle/backfill":  "connect Amazon before running a backfill",
		"/api/v1/kindle/reconcile": "connect Amazon before reconciling Kindle status",
		"/api/v1/audible/backfill": "connect Amazon before running a backfill",
		"/api/v1/books/sync-all":   "connect Amazon before running a full sync",
	}
	enq := &stubEnqueuer{}
	e := booksCredRouter(hz, enq)
	for _, path := range credPaths {
		rec := postAs(e, path, token)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 for a user with no credential (body: %s)",
				path, rec.Code, rec.Body.String())
		}
		var env map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: body is not the JSON error envelope: %s", path, rec.Body.String())
		}
		if got, _ := env["error"].(string); got != wantMsg[path] {
			t.Fatalf("%s: error = %q, want %q", path, got, wantMsg[path])
		}
	}
	if enq.calls != 0 {
		t.Fatalf("a job was enqueued (%d) for a user with no Amazon credential", enq.calls)
	}
}
