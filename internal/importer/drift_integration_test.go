// drift_integration_ginkgo_test.go — ginkgo mirror of drift_integration_test.go (gaka-0vp).
// 1:1 case map (2 stdlib TestXxx):
//
//	TestDriftEndToEndUnknownFieldPersisted → drift end-to-end > "unknown field on heartbeats is persisted to import_jobs.drift with a warn log"
//	TestDriftEndToEndBrokenLookupFailsJob  → drift end-to-end > "missing 'value' on user_agents hard-fails the job and persists drift"
//
// Ginkgo-native helpers openDriftDBGinkgo + mockWakatime.startGinkgo mirror
// the stdlib helpers openDriftDB + mockWakatime.start; both accept no
// *testing.T (they use DeferCleanup + Skip). The remaining plumbing —
// dedicatedDriftURL, ensureDedicatedDB, driftDSN, mockWakatime struct — is
// shared with the stdlib file.
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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// openDriftDBGinkgo mirrors openDriftDB but uses ginkgo Skip/DeferCleanup
// instead of *testing.T.
func openDriftDBGinkgo() *db.DB {
	targetURL := dedicatedDriftURL()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := ensureDedicatedDB(ctx, targetURL); err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			Fail("ensure " + targetURL + ": " + err.Error())
		}
		Skip("cannot provision drift test DB: " + err.Error())
	}
	if err := db.MigrateURL(ctx, targetURL); err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			Fail("migrate: " + err.Error())
		}
		Skip("migrate failed: " + err.Error())
	}
	database, err := db.New(ctx, targetURL)
	if err != nil {
		if os.Getenv("BOOM_REQUIRE_DB") == "1" {
			Fail("db.New: " + err.Error())
		}
		Skip("db.New: " + err.Error())
	}
	DeferCleanup(database.Close)
	return database
}

// startMockWakatimeGinkgo mirrors mockWakatime.start but uses DeferCleanup.
func startMockWakatimeGinkgo(m *mockWakatime) *httptest.Server {
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
	DeferCleanup(srv.Close)
	return srv
}

var _ = Describe("drift end-to-end (gaka-unq.1)", func() {
	It("unknown field on heartbeats is persisted to import_jobs.drift with a warn log", func() {
		database := openDriftDBGinkgo()
		ctx := context.Background()

		// One user (heartbeats FK requires the user row + a project).
		owner := "drift_e2e_gk_" + time.Now().Format("150405.000000")
		_, _ = database.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)
		DeferCleanup(func() {
			bg := context.Background()
			_, _ = database.Pool.Exec(bg, `DELETE FROM heartbeats WHERE sender=$1`, owner)
			_, _ = database.Pool.Exec(bg, `DELETE FROM projects WHERE owner=$1`, owner)
			_, _ = database.Pool.Exec(bg, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner=$1)`, owner)
			_, _ = database.Pool.Exec(bg, `DELETE FROM import_jobs WHERE owner=$1`, owner)
			_, _ = database.Pool.Exec(bg, `DELETE FROM users WHERE username=$1`, owner)
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
		srv := startMockWakatimeGinkgo(m)

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
		Expect(err).NotTo(HaveOccurred())

		// Run inline (StartJob would race the test; call run directly).
		worker.run(ctx, job.ID, item)

		// Fetch persisted state.
		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final).NotTo(BeNil())
		Expect(final.State).To(Equal(db.JobStateCompleted), "error=%v", final.Error)
		Expect(len(final.Drift)).To(BeNumerically(">", 0), "expected persisted drift, got empty")

		var findings []DriftFinding
		Expect(json.Unmarshal(final.Drift, &findings)).To(Succeed())
		found := false
		for _, f := range findings {
			if f.Endpoint == "heartbeats" && f.Field == "brand_new_wakatime_field" && f.Kind == driftKindUnknown {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "expected unknown_field for brand_new_wakatime_field, got %+v", findings)

		// Verify a warn log line was appended for the drift.
		logs, err := database.GetJobLogs(ctx, job.ID, 0, 1000)
		Expect(err).NotTo(HaveOccurred())
		sawWarn := false
		for _, l := range logs {
			if l.Level == "warn" && strings.Contains(l.Message, "schema drift") {
				sawWarn = true
				break
			}
		}
		Expect(sawWarn).To(BeTrue(), "expected a warn schema-drift log line, got %+v", logs)
	})

	It("missing 'value' on user_agents hard-fails the job and persists drift", func() {
		database := openDriftDBGinkgo()
		ctx := context.Background()

		owner := "drift_e2e_fail_gk_" + time.Now().Format("150405.000000")
		_, _ = database.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)
		DeferCleanup(func() {
			bg := context.Background()
			_, _ = database.Pool.Exec(bg, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner=$1)`, owner)
			_, _ = database.Pool.Exec(bg, `DELETE FROM import_jobs WHERE owner=$1`, owner)
			_, _ = database.Pool.Exec(bg, `DELETE FROM users WHERE username=$1`, owner)
		})

		// user_agents payload is missing `value` on every entry.
		uaBody := `{"data":[{"id":"ua-1"}]}`
		mnBody := `{"data":[]}`
		m := &mockWakatime{uaBody: uaBody, mnBody: mnBody, defaultHB: `{"data":[]}`}
		srv := startMockWakatimeGinkgo(m)

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
		Expect(err).NotTo(HaveOccurred())
		worker.run(ctx, job.ID, item)

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final).NotTo(BeNil())
		Expect(final.State).To(Equal(db.JobStateFailed))
		Expect(len(final.Drift)).To(BeNumerically(">", 0), "expected persisted drift on failed job")
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
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

type mockWakatime struct {
	uaBody      string
	mnBody      string
	hbBodyByDay map[string]string
	defaultHB   string
}
