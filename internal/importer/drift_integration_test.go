package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// gaka-unq.1: end-to-end drift persistence test — runs a real worker with an
// httptest.Server standing in for wakatime.com and asserts that drift findings
// are persisted on the import_jobs row and that a warn-level log line is
// written. Uses a DEDICATED per-package database (boomtime_test_drift) so a
// prior partial migration state on the shared test DB can't skew results.

const (
	defaultDriftDSN     = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
	dedicatedDriftDBSfx = "_drift"
)

func driftDSN() string {
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultDriftDSN
}

// dedicatedDriftURL swaps the DB name to `<original>_drift`. This isolation
// insulates the drift tests from any prior "current version = 14" state on
// the shared test DB where the drift column may be missing (e.g. a previous
// 00014 file with different content).
func dedicatedDriftURL() string {
	url := driftDSN()
	q := ""
	if i := strings.Index(url, "?"); i >= 0 {
		q = url[i:]
		url = url[:i]
	}
	slash := strings.LastIndex(url, "/")
	if slash < 0 {
		return url + q
	}
	return url[:slash+1] + url[slash+1:] + dedicatedDriftDBSfx + q
}

// maintenanceURL swaps the DB name in url for maintDB.
func maintenanceURL(url, maintDB string) string {
	q := ""
	if i := strings.Index(url, "?"); i >= 0 {
		q = url[i:]
		url = url[:i]
	}
	slash := strings.LastIndex(url, "/")
	if slash < 0 {
		return url + q
	}
	return url[:slash+1] + maintDB + q
}

// openDriftDB opens a DEDICATED test DB and migrates it. Skips (unless
// BOOM_REQUIRE_DB=1) if Postgres is unreachable.
func openDriftDB(t *testing.T) *db.DB {
	t.Helper()
	targetURL := dedicatedDriftURL()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Ensure the dedicated DB exists via a maintenance connection.
	if err := ensureDedicatedDB(ctx, targetURL); err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("ensure %s: %v", targetURL, err)
		}
		t.Skipf("skipping: cannot provision drift test DB: %v", err)
	}

	// 2. Migrate — the dedicated DB has no prior goose state, so 00014 runs
	//    fresh and adds the drift column.
	if err := db.MigrateURL(ctx, targetURL); err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("migrate: %v", err)
		}
		t.Skipf("skipping: migrate failed: %v", err)
	}
	database, err := db.New(ctx, targetURL)
	if err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			t.Fatalf("db.New: %v", err)
		}
		t.Skipf("skipping: db.New: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

// ensureDedicatedDB creates the target database if it doesn't already exist,
// via a maintenance connection.
func ensureDedicatedDB(ctx context.Context, targetURL string) error {
	// Extract target dbname.
	url := targetURL
	q := ""
	if i := strings.Index(url, "?"); i >= 0 {
		q = url[i:]
		url = url[:i]
	}
	slash := strings.LastIndex(url, "/")
	if slash < 0 {
		return fmt.Errorf("bad DSN: %s", targetURL)
	}
	target := url[slash+1:]
	_ = q

	var lastErr error
	for _, maint := range []string{"postgres", "test"} {
		mURL := maintenanceURL(targetURL, maint)
		pool, err := pgxpool.New(ctx, mURL)
		if err != nil {
			lastErr = err
			continue
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			lastErr = err
			continue
		}
		_, err = pool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, strings.ReplaceAll(target, `"`, `""`)))
		pool.Close()
		if err == nil || strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "42P04") {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no reachable maintenance database")
	}
	return lastErr
}

// mockWakatime builds an httptest server that serves the requested per-endpoint
// bodies. It matches URL paths on suffix so query strings on /heartbeats don't
// interfere.
type mockWakatime struct {
	uaBody      string
	mnBody      string
	hbBodyByDay map[string]string
	defaultHB   string
}

func (m *mockWakatime) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users/current/user_agents", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, m.uaBody)
	})
	mux.HandleFunc("/api/v1/users/current/machine_names", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, m.mnBody)
	})
	mux.HandleFunc("/api/v1/users/current/heartbeats", func(w http.ResponseWriter, r *http.Request) {
		day := r.URL.Query().Get("date")
		body, ok := m.hbBodyByDay[day]
		if !ok {
			body = m.defaultHB
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestDriftEndToEndUnknownFieldPersisted runs a real worker against a mock
// wakatime server that returns an unknown field on heartbeats. Asserts that
// (a) the job completes, (b) drift[] is persisted on the row, (c) a "warn"
// log line was appended, and (d) it rides along on GetJobByID (WS/REST both
// serve this).
func TestDriftEndToEndUnknownFieldPersisted(t *testing.T) {
	database := openDriftDB(t)
	ctx := context.Background()

	// One user (heartbeats FK requires the user row + a project).
	owner := "drift_e2e_" + time.Now().Format("150405.000000")
	_, _ = database.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, owner)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, owner)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner=$1)`, owner)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner=$1`, owner)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, owner)
	})

	// Wakatime mock: clean UA/MN, heartbeats with unknown field.
	uaBody := `{"data":[{"id":"ua-1","value":"vscode-test/1.0 (mac) my-editor/1.0"}]}`
	mnBody := `{"data":[{"id":"mn-1","value":"my-mac"}]}`
	hbBody := `{"data":[
      {
        "user_agent_id":"ua-1",
        "machine_name_id":"mn-1",
        "entity":"/tmp/a.go",
        "type":"file",
        "time":1735689600.0,
        "brand_new_wakatime_field":"drift"
      }
    ]}`
	m := &mockWakatime{uaBody: uaBody, mnBody: mnBody, defaultHB: hbBody}
	srv := m.start(t)

	// Build a worker pointed at the mock.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub()
	worker := NewWorker(context.Background(), database, logger, hub)
	worker.BaseURL = srv.URL

	// Create a queued job.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start
	payload := model.ImportRequestPayload{APIToken: "test-token", StartDate: start, EndDate: end}
	item := QueueItem{Requester: owner, ReqPayload: payload}
	raw, _ := json.Marshal(item)
	job, err := database.CreateImportJob(ctx, owner, raw, start, end, TotalDays(start, end))
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}

	// Run inline (StartJob would race the test; call run directly).
	worker.run(ctx, job.ID, item)

	// Fetch persisted state.
	final, err := database.GetJobByID(ctx, job.ID)
	if err != nil || final == nil {
		t.Fatalf("GetJobByID: %v (job=%v)", err, final)
	}
	if final.State != db.JobStateCompleted {
		t.Fatalf("state = %q, want completed. error=%v", final.State, final.Error)
	}
	if len(final.Drift) == 0 {
		t.Fatal("expected persisted drift, got empty")
	}
	var findings []DriftFinding
	if err := json.Unmarshal(final.Drift, &findings); err != nil {
		t.Fatalf("unmarshal drift: %v (%s)", err, string(final.Drift))
	}
	found := false
	for _, f := range findings {
		if f.Endpoint == "heartbeats" && f.Field == "brand_new_wakatime_field" && f.Kind == driftKindUnknown {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown_field for brand_new_wakatime_field, got %+v", findings)
	}

	// Verify a warn log line was appended for the drift.
	logs, err := database.GetJobLogs(ctx, job.ID, 0, 1000)
	if err != nil {
		t.Fatalf("GetJobLogs: %v", err)
	}
	sawWarn := false
	for _, l := range logs {
		if l.Level == "warn" && strings.Contains(l.Message, "schema drift") {
			sawWarn = true
			break
		}
	}
	if !sawWarn {
		t.Fatalf("expected a warn schema-drift log line, got %+v", logs)
	}
}

// TestDriftEndToEndBrokenLookupFailsJob asserts that when a required field is
// missing on user_agents (id/value), the job hard-fails — heartbeat ingestion
// cannot resolve UAs otherwise.
func TestDriftEndToEndBrokenLookupFailsJob(t *testing.T) {
	database := openDriftDB(t)
	ctx := context.Background()

	owner := "drift_e2e_fail_" + time.Now().Format("150405.000000")
	_, _ = database.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)
	t.Cleanup(func() {
		_, _ = database.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner=$1)`, owner)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner=$1`, owner)
		_, _ = database.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, owner)
	})

	// user_agents payload is missing `value` on every entry.
	uaBody := `{"data":[{"id":"ua-1"}]}`
	mnBody := `{"data":[]}`
	m := &mockWakatime{uaBody: uaBody, mnBody: mnBody, defaultHB: `{"data":[]}`}
	srv := m.start(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub()
	worker := NewWorker(context.Background(), database, logger, hub)
	worker.BaseURL = srv.URL

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start
	payload := model.ImportRequestPayload{APIToken: "test-token", StartDate: start, EndDate: end}
	item := QueueItem{Requester: owner, ReqPayload: payload}
	raw, _ := json.Marshal(item)
	job, err := database.CreateImportJob(ctx, owner, raw, start, end, TotalDays(start, end))
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	worker.run(ctx, job.ID, item)

	final, err := database.GetJobByID(ctx, job.ID)
	if err != nil || final == nil {
		t.Fatalf("GetJobByID: %v (job=%v)", err, final)
	}
	if final.State != db.JobStateFailed {
		t.Fatalf("state = %q, want failed", final.State)
	}
	if len(final.Drift) == 0 {
		t.Fatal("expected persisted drift on failed job")
	}
}
