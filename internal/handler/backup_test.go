package handler_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// doRaw issues a request with a raw (non-JSON) body against the harness router.
func doRaw(t *testing.T, e http.Handler, method, target, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestDBImportGuards(t *testing.T) {
	// Isolated DB: the active-import rejection scans import_jobs across ALL
	// owners, so leftover jobs on the shared test DB would flip answers.
	hz := testutil.NewHarnessWithDB(t, testutil.OpenIsolatedDB(t, "backup"))
	e := hz.Router()
	_, token := hz.MintUser("backupguard")

	// Neutralize any queued/running jobs a previously-aborted run left behind.
	if _, err := hz.DB.MarkRunningJobsFailed(t.Context(), "backup_test cleanup"); err != nil {
		t.Fatalf("cleanup running jobs: %v", err)
	}

	// No auth header → 400 (missing auth), bogus token → 403.
	if rec := doRaw(t, e, http.MethodPost, "/api/v1/users/current/db/import?confirm=replace-all-data", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("no auth: status %d, want 400", rec.Code)
	}
	if rec := doRaw(t, e, http.MethodGet, "/api/v1/users/current/db/export", "bogus", nil); rec.Code != http.StatusForbidden {
		t.Errorf("bad token export: status %d, want 403", rec.Code)
	}

	// Auth'd but missing the confirm param → 400, and nothing is truncated.
	user2, _ := hz.MintUser("backupguard2")
	if rec := doRaw(t, e, http.MethodPost, "/api/v1/users/current/db/import", token, []byte("zipzip")); rec.Code != http.StatusBadRequest {
		t.Errorf("missing confirm: status %d, want 400", rec.Code)
	}
	var n int
	if err := hz.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE username=$1`, user2).Scan(&n); err != nil || n != 1 {
		t.Errorf("guarded import touched data (n=%d, err=%v)", n, err)
	}

	// Confirmed but not a zip → 400.
	if rec := doRaw(t, e, http.MethodPost, "/api/v1/users/current/db/import?confirm=replace-all-data", token, []byte("not a zip")); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage archive: status %d, want 400", rec.Code)
	}
}

// TestDBBackupRoundTripHTTP drives export → mutate → import over HTTP and
// asserts the restored state and summary shape.
func TestDBBackupRoundTripHTTP(t *testing.T) {
	// The import TRUNCATEs every table — run on a DEDICATED database so
	// parallel test packages sharing the main test DB are never wiped.
	hz := testutil.NewHarnessWithDB(t, testutil.OpenIsolatedDB(t, "backup"))
	e := hz.Router()
	user, token := hz.MintUser("backuprt")

	base := time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects("alpha")
	sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 3, 60)

	var beatsBefore int64
	if err := hz.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM heartbeats`).Scan(&beatsBefore); err != nil {
		t.Fatal(err)
	}

	// Export.
	rec := doRaw(t, e, http.MethodGet, "/api/v1/users/current/db/export", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("export content-type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd == "" {
		t.Errorf("export missing Content-Disposition")
	}
	archive := rec.Body.Bytes()
	if _, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("export did not produce a valid zip: %v", err)
	}

	// Mutate after the export; the import must roll this back.
	sd.Seed(testutil.HB{Project: "alpha", Entity: "extra.go", TS: base.Add(48 * time.Hour)})

	// Import.
	rec = doRaw(t, e, http.MethodPost, "/api/v1/users/current/db/import?confirm=replace-all-data", token, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: status %d body=%s", rec.Code, rec.Body.String())
	}
	var summary struct {
		GooseVersion int64            `json:"gooseVersion"`
		TotalRows    int64            `json:"totalRows"`
		Tables       map[string]int64 `json:"tables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("summary decode: %v body=%s", err, rec.Body.String())
	}
	if summary.Tables["heartbeats"] != beatsBefore {
		t.Errorf("summary heartbeats = %d, want %d", summary.Tables["heartbeats"], beatsBefore)
	}
	if summary.GooseVersion == 0 || summary.TotalRows == 0 {
		t.Errorf("suspicious summary: %+v", summary)
	}

	var beatsAfter int64
	if err := hz.DB.Pool.QueryRow(t.Context(), `SELECT count(*) FROM heartbeats`).Scan(&beatsAfter); err != nil {
		t.Fatal(err)
	}
	if beatsAfter != beatsBefore {
		t.Errorf("heartbeats after restore = %d, want %d (post-export mutation must be gone)", beatsAfter, beatsBefore)
	}

	// The token used for the request was part of the dump, so it still works.
	if rec := doRaw(t, e, http.MethodGet, "/api/v1/users/current/db/export", token, nil); rec.Code != http.StatusOK {
		t.Errorf("token no longer valid after restoring its own backup: %d", rec.Code)
	}
}
