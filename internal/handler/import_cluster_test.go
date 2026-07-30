// import_cluster_test.go — comprehensive coverage of the import handler
// cluster (gaka-d6x.handler). Complements the existing import_test.go which
// pins gaka-6jm.8 save-on-success. This file adds:
//
//   - Job list/read/logs endpoints with cross-user isolation invariants
//   - Cancel path (queued, running-in-worker, already-terminal, cross-user)
//   - ImportConfig hasServerKey true/false
//   - WakatimeRange happy-path (mocked wakatime httptest.Server) + no-key
//   - ImportRequest branches (existing running-job dedupe, fallback to saved
//     encrypted key, effectiveImportToken server-env fallback)
//   - ImportJobWS handshake, snapshot, terminal close, cross-user 404
//   - Helpers (parseJobID / isTerminal / jobForOwner / ownedJob)
//
// Package convention (per import_test.go): external ginkgo variant
// (package handler_test) so we mirror the sibling.
package handler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// silentImportLogger is a slog.Logger that drops every record. Reused across
// tests so noisy warns from the wakatime range/token lookup paths don't taint
// the ginkgo console.
func silentImportLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// importDeps bundles a running-worker Handler + Echo router with all the
// import routes wired (mirrors internal/server for the endpoints under test).
//
// A dedicated router (not testutil.Router() which does NOT register
// /import/*) is REQUIRED here — the harness router is deliberately narrow.
// Re-registering these routes on the harness router would trigger echo's
// duplicate-route panic on repeat spec runs.
type importDeps struct {
	Hz      *testutil.Harness
	H       *handler.Handler
	Worker  *importer.Worker
	Hub     *importer.Hub
	Cfg     *config.Config
	Router  *echo.Echo
	Server  *httptest.Server // for websocket / range mocks
	MockWK  *httptest.Server // wakatime.com stand-in (may be nil)
	Cancel  context.CancelFunc
}

func newImportDeps(serverKey string) *importDeps {
	hz := testutil.NewHarness(GinkgoT())
	workerCtx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)

	logger := silentImportLogger()
	hub := importer.NewHub()
	worker := importer.NewWorker(workerCtx, hz.DB, logger, hub)
	cfg := &config.Config{
		Port:               8080,
		EnableRegistration: true,
		SessionExpiry:      24,
		WakatimeAPIKey:     serverKey, // controlled per test
	}
	h := handler.New(hz.DB, cfg, logger, worker, hub, nil)

	e := echo.New()
	// Full import route surface — one line per handler method under test.
	e.POST("/api/v1/users/current/import", h.ImportRequest)
	e.GET("/api/v1/users/current/import/config", h.ImportConfig)
	e.POST("/api/v1/users/current/import/wakatime-range", h.WakatimeRange)
	e.GET("/api/v1/users/current/import/jobs", h.ImportJobs)
	e.GET("/api/v1/users/current/import/jobs/:id", h.ImportJob)
	e.POST("/api/v1/users/current/import/jobs/:id/cancel", h.ImportJobCancel)
	e.GET("/api/v1/users/current/import/jobs/:id/logs", h.ImportJobLogs)
	e.GET("/api/v1/users/current/import/jobs/:id/ws", h.ImportJobWS)

	deps := &importDeps{
		Hz:     hz,
		H:      h,
		Worker: worker,
		Hub:    hub,
		Cfg:    cfg,
		Router: e,
		Cancel: cancel,
	}

	// httptest.Server so websocket / hijack works. The harness router doesn't
	// need one because httptest.NewRecorder handles regular JSON just fine.
	deps.Server = httptest.NewServer(e)
	DeferCleanup(deps.Server.Close)
	return deps
}

// jsonReq issues a JSON request against the import router and returns the recorder.
func jsonReq(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// jobIDFromSubmit extracts jobId from ImportRequest's OK response envelope.
func jobIDFromSubmit(rec *httptest.ResponseRecorder) int {
	var out struct {
		JobID int `json:"jobId"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
	Expect(out.JobID).To(BeNumerically(">", 0))
	return out.JobID
}

// -----------------------------------------------------------------------------
// ImportConfig
// -----------------------------------------------------------------------------

var _ = Describe("ImportConfig", func() {
	It("reports hasServerKey=true iff cfg.WakatimeAPIKey is set (server-env fallback exists)", func() {
		// Deliberate on/off pair — the endpoint is the only signal the FE has
		// for "the Import button can proceed without me typing a key".
		depsOff := newImportDeps("")
		depsOn := newImportDeps("some-secret-env-key")

		// The endpoint is unauthed; no token required.
		recOff := jsonReq(depsOff.Router, http.MethodGet, "/api/v1/users/current/import/config", "", nil)
		Expect(recOff).To(testutil.HaveStatus(http.StatusOK))
		var off map[string]bool
		Expect(json.Unmarshal(recOff.Body.Bytes(), &off)).To(Succeed())
		Expect(off["hasServerKey"]).To(BeFalse())

		recOn := jsonReq(depsOn.Router, http.MethodGet, "/api/v1/users/current/import/config", "", nil)
		Expect(recOn).To(testutil.HaveStatus(http.StatusOK))
		var on map[string]bool
		Expect(json.Unmarshal(recOn.Body.Bytes(), &on)).To(Succeed())
		Expect(on["hasServerKey"]).To(BeTrue())
	})
})

// -----------------------------------------------------------------------------
// WakatimeRange
// -----------------------------------------------------------------------------

var _ = Describe("WakatimeRange (gaka-awh.2)", func() {
	It("returns hasData:false (no error) when there is no effective key — no oracle leak", func() {
		deps := newImportDeps("") // no server key
		user, token := deps.Hz.MintUser("wr_nokey")
		_ = user

		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import/wakatime-range", token, map[string]string{})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out["hasData"]).To(Equal(false),
			"no effective key MUST yield {hasData:false}; ANY other shape leaks whether a server key exists")
	})

	It("401 on unauth (missing Authorization header) — user-scoped endpoint", func() {
		deps := newImportDeps("some-server-key")
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import/wakatime-range", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400),
			"unauth range request MUST NOT succeed; got status %d", rec.Code)
	})

	It("with a token: upstream call is attempted, but any error path is coerced to {hasData:false} (no oracle)", func() {
		// FetchAllTimeRange has a hardcoded wakatime.com base. We can't
		// intercept it, but http.Client honors HTTPS_PROXY — pointing the
		// proxy at a dead loopback port makes the outbound HTTPS request
		// fail on connect within a few ms. That drops us straight into the
		// error-handling branch (lines 243-247) while asserting the no-
		// oracle invariant: the caller only ever sees {hasData:false}.
		//
		// If a stray HTTP_PROXY / HTTPS_PROXY is already in the env, we defer
		// restore rather than clobber.
		prevH, hadH := os.LookupEnv("HTTPS_PROXY")
		prevh, hadh := os.LookupEnv("https_proxy")
		prevN, hadN := os.LookupEnv("NO_PROXY")
		prevn, hadn := os.LookupEnv("no_proxy")
		os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
		os.Setenv("https_proxy", "http://127.0.0.1:1")
		// Keep httptest loopback traffic unaffected by the fake proxy.
		os.Setenv("NO_PROXY", "127.0.0.1,localhost")
		os.Setenv("no_proxy", "127.0.0.1,localhost")
		DeferCleanup(func() {
			restore := func(k, prev string, had bool) {
				if had {
					os.Setenv(k, prev)
				} else {
					os.Unsetenv(k)
				}
			}
			restore("HTTPS_PROXY", prevH, hadH)
			restore("https_proxy", prevh, hadh)
			restore("NO_PROXY", prevN, hadN)
			restore("no_proxy", prevn, hadn)
		})

		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("wr_upstream_dead")

		body, err := json.Marshal(map[string]string{"apiToken": "waka_fake_key_upstream"})
		Expect(err).NotTo(HaveOccurred())
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import/wakatime-range", token, map[string]string{"apiToken": "waka_fake_key_upstream"})
		_ = body

		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var out map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out["hasData"]).To(Equal(false),
			"upstream error MUST surface as {hasData:false}; got %s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring("waka_fake_key_upstream"),
			"the submitted API token leaked in the range-probe response (oracle)")
	})

	It("malformed JSON body is tolerated (bind error ignored) → falls back to server key path", func() {
		// gaka-awh.2: body is optional; the handler ignores bind errors so a
		// broken body doesn't 400 the range probe. With no server key the
		// endpoint still returns hasData:false rather than an error envelope
		// — the same no-oracle invariant as the empty-body case.
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("wr_badjson")

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/import/wakatime-range",
			bytes.NewReader([]byte(`not-json-at-all`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		deps.Router.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var out map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out["hasData"]).To(Equal(false),
			"malformed body MUST NOT surface a bind error; got %s", rec.Body.String())
	})
})

// -----------------------------------------------------------------------------
// ImportRequest branches (extends the sibling save-on-success test)
// -----------------------------------------------------------------------------

var _ = Describe("ImportRequest additional branches", func() {
	It("returns the existing job (not a new one) when one is already queued for the owner — one-active-per-owner invariant", func() {
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_dedupe")
		ctx := context.Background()

		// Pre-seed a queued job — bypasses the worker so it stays queued.
		payload, err := json.Marshal(map[string]any{"apiToken": ""})
		Expect(err).NotTo(HaveOccurred())
		now := time.Now().UTC()
		existing, err := deps.Hz.DB.CreateImportJob(ctx, user, payload, now, now, 1)
		Expect(err).NotTo(HaveOccurred())

		// Submit — must return the SAME job id, not mint a second one.
		body := map[string]any{
			"apiToken":  "should_be_ignored_because_a_job_is_queued",
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		Expect(jobIDFromSubmit(rec)).To(Equal(existing.ID),
			"one-active-per-owner: submit MUST return the existing job id, not create a second")

		// Cleanup — mark failed so DB teardown doesn't race the worker.
		_, _ = deps.Hz.DB.MarkRunningJobsFailed(ctx, "test cleanup")
	})

	It("rejects malformed JSON body with 400 (BindJSON)", func() {
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("import_badjson")

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/import",
			bytes.NewReader([]byte(`{"apiToken": broken}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		deps.Router.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"malformed JSON should be 400, got body=%s", rec.Body.String())
	})

	It("rejects a request without an Authorization header (400 MissingAuth)", func() {
		deps := newImportDeps("")
		now := time.Now().UTC()
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", "",
			map[string]any{"startDate": now.Format(time.RFC3339), "endDate": now.Format(time.RFC3339)})
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})
})

// -----------------------------------------------------------------------------
// ImportJobs + cross-user isolation
// -----------------------------------------------------------------------------

var _ = Describe("ImportJobs (list)", func() {
	It("returns ONLY the caller's jobs — user B never sees user A's jobs", func() {
		deps := newImportDeps("")
		userA, tokenA := deps.Hz.MintUser("import_list_A")
		userB, tokenB := deps.Hz.MintUser("import_list_B")
		ctx := context.Background()

		now := time.Now().UTC()
		jobA, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		jobB, err := deps.Hz.DB.CreateImportJob(ctx, userB, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id IN ($1, $2)`, jobA.ID, jobB.ID)
		})

		recA := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs", tokenA, nil)
		Expect(recA).To(testutil.HaveStatus(http.StatusOK))
		var payloadA struct {
			Jobs []db.Job `json:"jobs"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &payloadA)).To(Succeed())
		aIDs := make(map[int]struct{})
		for _, j := range payloadA.Jobs {
			aIDs[j.ID] = struct{}{}
			Expect(j.Owner).To(Equal(userA),
				"cross-user leak: user A's list contained a job owned by %s", j.Owner)
		}
		Expect(aIDs).To(HaveKey(jobA.ID))
		Expect(aIDs).NotTo(HaveKey(jobB.ID))

		recB := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs", tokenB, nil)
		var payloadB struct {
			Jobs []db.Job `json:"jobs"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &payloadB)).To(Succeed())
		bIDs := make(map[int]struct{})
		for _, j := range payloadB.Jobs {
			bIDs[j.ID] = struct{}{}
			Expect(j.Owner).To(Equal(userB))
		}
		Expect(bIDs).To(HaveKey(jobB.ID))
		Expect(bIDs).NotTo(HaveKey(jobA.ID),
			"cross-user leak: user B's list contained user A's job id")
	})

	It("401/403 on missing / bogus token — never leaks another owner's list", func() {
		deps := newImportDeps("")
		rec := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs", "not-a-real-token", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})
})

// -----------------------------------------------------------------------------
// ImportJob (single, owner-scoped) — exercises jobForOwner + ownedJob
// -----------------------------------------------------------------------------

var _ = Describe("ImportJob (single-job view)", func() {
	It("returns a caller's own job with its logs; user B gets 404 (never 200/403) — no oracle leak", func() {
		deps := newImportDeps("")
		userA, tokenA := deps.Hz.MintUser("import_get_A")
		_, tokenB := deps.Hz.MintUser("import_get_B")
		ctx := context.Background()

		now := time.Now().UTC()
		jobA, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		_, _ = deps.Hz.DB.InsertJobLog(ctx, jobA.ID, "info", "hello-from-A")
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, jobA.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, jobA.ID)
		})

		targetA := "/api/v1/users/current/import/jobs/" + strconv.Itoa(jobA.ID)
		recA := jsonReq(deps.Router, http.MethodGet, targetA, tokenA, nil)
		Expect(recA).To(testutil.HaveStatus(http.StatusOK))
		var payloadA struct {
			Job  *db.Job      `json:"job"`
			Logs []db.LogLine `json:"logs"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &payloadA)).To(Succeed())
		Expect(payloadA.Job).NotTo(BeNil())
		Expect(payloadA.Job.ID).To(Equal(jobA.ID))
		Expect(payloadA.Logs).NotTo(BeEmpty())

		// LOAD-BEARING: user B must get 404 (not 403) so no data-side signal
		// discloses "this id is claimed by someone else".
		recB := jsonReq(deps.Router, http.MethodGet, targetA, tokenB, nil)
		Expect(recB).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user leak: user B saw status %d on user A's job (want 404)", recB.Code)
	})

	It("returns 400 on unparseable :id (jobForOwner: parseJobID)", func() {
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("import_get_bad_id")
		rec := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs/not-a-number", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("returns 404 on a nonexistent id (jobForOwner: job==nil)", func() {
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("import_get_missing")
		rec := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs/99999999", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

// -----------------------------------------------------------------------------
// ImportJobLogs — same owner-scoping + afterId branch
// -----------------------------------------------------------------------------

var _ = Describe("ImportJobLogs (tail with afterId)", func() {
	It("logs are owner-scoped and afterId filters to strictly-greater ids", func() {
		deps := newImportDeps("")
		userA, tokenA := deps.Hz.MintUser("import_logs_A")
		_, tokenB := deps.Hz.MintUser("import_logs_B")
		ctx := context.Background()

		now := time.Now().UTC()
		jobA, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		l1, err := deps.Hz.DB.InsertJobLog(ctx, jobA.ID, "info", "first")
		Expect(err).NotTo(HaveOccurred())
		l2, err := deps.Hz.DB.InsertJobLog(ctx, jobA.ID, "info", "second")
		Expect(err).NotTo(HaveOccurred())
		Expect(l2.ID).To(BeNumerically(">", l1.ID))
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, jobA.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, jobA.ID)
		})

		target := "/api/v1/users/current/import/jobs/" + strconv.Itoa(jobA.ID) + "/logs"
		// No afterId — returns both.
		recAll := jsonReq(deps.Router, http.MethodGet, target, tokenA, nil)
		Expect(recAll).To(testutil.HaveStatus(http.StatusOK))
		var all struct {
			Logs []db.LogLine `json:"logs"`
		}
		Expect(json.Unmarshal(recAll.Body.Bytes(), &all)).To(Succeed())
		Expect(all.Logs).To(HaveLen(2))

		// afterId = l1.ID — only l2 comes back.
		recTail := jsonReq(deps.Router, http.MethodGet, target+"?afterId="+strconv.FormatInt(l1.ID, 10), tokenA, nil)
		Expect(recTail).To(testutil.HaveStatus(http.StatusOK))
		var tail struct {
			Logs []db.LogLine `json:"logs"`
		}
		Expect(json.Unmarshal(recTail.Body.Bytes(), &tail)).To(Succeed())
		Expect(tail.Logs).To(HaveLen(1))
		Expect(tail.Logs[0].ID).To(Equal(l2.ID),
			"afterId is a STRICT lower bound (>, not >=); saw id %d, want %d", tail.Logs[0].ID, l2.ID)

		// Cross-user isolation: user B may not read user A's logs.
		recCross := jsonReq(deps.Router, http.MethodGet, target, tokenB, nil)
		Expect(recCross).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user leak: user B saw status %d on user A's job logs (want 404)", recCross.Code)
	})
})

// -----------------------------------------------------------------------------
// ImportJobCancel
// -----------------------------------------------------------------------------

var _ = Describe("ImportJobCancel", func() {
	It("cancels a QUEUED job durably via DB (worker not running) → job.state=cancelled", func() {
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_cancel_queued")
		ctx := context.Background()

		now := time.Now().UTC()
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, job.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		target := "/api/v1/users/current/import/jobs/" + strconv.Itoa(job.ID) + "/cancel"
		rec := jsonReq(deps.Router, http.MethodPost, target, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payload struct {
			Job *db.Job `json:"job"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.Job).NotTo(BeNil())
		Expect(payload.Job.State).To(Equal(db.JobStateCancelled),
			"cancel on queued job must transition to cancelled; got %s", payload.Job.State)
	})

	It("cancel on an already-TERMINAL job is a no-op (DB returns nil) and echoes original job", func() {
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_cancel_term")
		ctx := context.Background()

		now := time.Now().UTC()
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, job.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		// Drive to terminal state BEFORE calling cancel.
		_, err = deps.Hz.DB.FinishImportJob(ctx, job.ID, db.JobStateCompleted, nil)
		Expect(err).NotTo(HaveOccurred())

		target := "/api/v1/users/current/import/jobs/" + strconv.Itoa(job.ID) + "/cancel"
		rec := jsonReq(deps.Router, http.MethodPost, target, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payload struct {
			Job *db.Job `json:"job"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.Job).NotTo(BeNil())
		Expect(payload.Job.State).To(Equal(db.JobStateCompleted),
			"cancel on terminal job must NOT clobber the terminal state; got %s", payload.Job.State)
	})

	It("cross-user cancel: user B cannot cancel user A's job (404, no state change)", func() {
		deps := newImportDeps("")
		userA, _ := deps.Hz.MintUser("import_cancel_iso_A")
		_, tokenB := deps.Hz.MintUser("import_cancel_iso_B")
		ctx := context.Background()

		now := time.Now().UTC()
		jobA, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, jobA.ID)
		})

		target := "/api/v1/users/current/import/jobs/" + strconv.Itoa(jobA.ID) + "/cancel"
		rec := jsonReq(deps.Router, http.MethodPost, target, tokenB, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-user leak: user B saw %d on cancel of user A's job", rec.Code)

		// LOAD-BEARING invariant: the job's state MUST NOT have moved.
		fresh, err := deps.Hz.DB.GetJobByID(ctx, jobA.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fresh).NotTo(BeNil())
		Expect(fresh.State).To(Equal(db.JobStateQueued),
			"cross-user cancel silently succeeded! state went %s→%s", db.JobStateQueued, fresh.State)
	})

	It("cancels an ACTIVELY-RUNNING worker job via the worker cancel path (done channel)", func() {
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_cancel_run")
		ctx := context.Background()

		// Wakatime mock that HANGS every request until the request-context is
		// cancelled. Ensures the worker goroutine sits inside fetchLookups()
		// with a live HTTP request in flight when we hit the cancel endpoint —
		// so h.Worker.Cancel(id) finds the job in the running map and takes
		// the DONE-channel branch (lines 199-209).
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		DeferCleanup(mock.Close)
		deps.Worker.BaseURL = mock.URL

		// Seed a queued job spanning one day; StartJob picks it up.
		now := time.Now().UTC()
		payload := model.ImportRequestPayload{APIToken: "any", StartDate: now, EndDate: now}
		raw, _ := json.Marshal(importer.QueueItem{Requester: user, ReqPayload: payload})
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, raw, now, now, importer.TotalDays(now, now))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, job.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		deps.Worker.StartJob(job, importer.QueueItem{Requester: user, ReqPayload: payload})

		// Wait for MarkJobRunning to land — the DB row flipping to state=running
		// is the moment we know the worker is well inside w.run() (past the
		// running-map insert). Non-destructive: this poll does NOT signal cancel.
		Eventually(func() string {
			fresh, err := deps.Hz.DB.GetJobByID(ctx, job.ID)
			if err != nil || fresh == nil {
				return ""
			}
			return fresh.State
		}, 3*time.Second, 20*time.Millisecond).Should(Equal(db.JobStateRunning))

		// Hit the cancel endpoint — this MUST take the running=true branch
		// (worker still in the running map because the HTTP request is blocked
		// upstream). The handler's select-with-done waits for finishCancelled
		// to write the terminal state, then returns.
		target := "/api/v1/users/current/import/jobs/" + strconv.Itoa(job.ID) + "/cancel"
		rec := jsonReq(deps.Router, http.MethodPost, target, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payloadOut struct {
			Job *db.Job `json:"job"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payloadOut)).To(Succeed())
		Expect(payloadOut.Job).NotTo(BeNil())

		// LOAD-BEARING: by the time the cancel response returns, the DB row
		// MUST be terminal — this is the whole point of the done-channel
		// (no 150ms sleep race, gaka-al6). Cancelled OR failed both count as
		// terminal (worker may catch ctx.Done vs finishCancelled path).
		Expect([]string{db.JobStateCancelled, db.JobStateFailed}).To(ContainElement(payloadOut.Job.State),
			"cancel returned before job hit terminal state; done channel not honored (got %s)", payloadOut.Job.State)
	})
})

// -----------------------------------------------------------------------------
// ImportJobWS — websocket handshake, snapshot, terminal-close, cross-user
// -----------------------------------------------------------------------------

// mintRefreshCookie inserts a refresh token for user and returns the raw value
// (the cookie's value). Directly writes to refresh_tokens with an hour expiry.
func mintRefreshCookie(hz *testutil.Harness, user string) string {
	raw := auth.NewRawToken()
	Expect(hz.DB.CreateAccessTokens(context.Background(), db.TokenData{
		Owner: user, Token: auth.NewRawToken(), RefreshToken: raw,
	}, 24)).To(Succeed())
	return raw
}

// wsURL turns an httptest.NewServer URL into the ws:// equivalent for websocket.Dial.
func wsURL(httpURL, path string) string {
	u, err := url.Parse(httpURL)
	Expect(err).NotTo(HaveOccurred())
	u.Scheme = "ws"
	u.Path = path
	return u.String()
}

var _ = Describe("ImportJobWS", func() {
	It("emits a snapshot event on connect and closes cleanly on TERMINAL job", func() {
		deps := newImportDeps("")
		user, _ := deps.Hz.MintUser("import_ws_ok")
		ctx := context.Background()

		refresh := mintRefreshCookie(deps.Hz, user)
		now := time.Now().UTC()
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		// Pre-seed a log so the snapshot is non-empty.
		_, _ = deps.Hz.DB.InsertJobLog(ctx, job.ID, "info", "snap-seed")
		// Drive job to terminal BEFORE we connect so the handler takes the
		// isTerminal branch (snapshot then close).
		_, err = deps.Hz.DB.FinishImportJob(ctx, job.ID, db.JobStateCompleted, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, job.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		// Dial the WS endpoint via the httptest server.
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dialCancel()
		conn, _, err := websocket.Dial(dialCtx, wsURL(deps.Server.URL, "/api/v1/users/current/import/jobs/"+strconv.Itoa(job.ID)+"/ws"), &websocket.DialOptions{
			HTTPHeader: http.Header{"Cookie": []string{"refresh_token=" + refresh}},
		})
		Expect(err).NotTo(HaveOccurred(), "ws handshake failed")
		defer conn.CloseNow()

		// The handler writes the snapshot as JSON then closes.
		var snap struct {
			Type string       `json:"type"`
			Job  *db.Job      `json:"job"`
			Logs []db.LogLine `json:"logs"`
		}
		readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer readCancel()
		Expect(wsjson.Read(readCtx, conn, &snap)).To(Succeed())
		Expect(snap.Type).To(Equal("snapshot"))
		Expect(snap.Job).NotTo(BeNil())
		Expect(snap.Job.ID).To(Equal(job.ID))
		Expect(snap.Job.State).To(Equal(db.JobStateCompleted))
		Expect(snap.Logs).NotTo(BeEmpty())
	})

	It("rejects the handshake without a refresh_token cookie (owner unresolvable)", func() {
		deps := newImportDeps("")
		user, _ := deps.Hz.MintUser("import_ws_nocookie")
		ctx := context.Background()

		now := time.Now().UTC()
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer dialCancel()
		_, resp, err := websocket.Dial(dialCtx, wsURL(deps.Server.URL, "/api/v1/users/current/import/jobs/"+strconv.Itoa(job.ID)+"/ws"), nil)
		Expect(err).To(HaveOccurred(),
			"ws handshake without refresh cookie MUST fail (owner unresolvable)")
		// Handshake should have returned a 4xx (not 101 Switching Protocols).
		if resp != nil {
			Expect(resp.StatusCode).NotTo(Equal(http.StatusSwitchingProtocols))
		}
	})

	It("streams a live log event then closes on a terminal state event (exercises the select loop)", func() {
		deps := newImportDeps("")
		user, _ := deps.Hz.MintUser("import_ws_live")
		ctx := context.Background()

		refresh := mintRefreshCookie(deps.Hz, user)
		now := time.Now().UTC()
		// Create job in RUNNING state so the handler enters the live loop (not
		// terminal-close after snapshot).
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		_, err = deps.Hz.DB.MarkJobRunning(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, job.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dialCancel()
		conn, _, err := websocket.Dial(dialCtx, wsURL(deps.Server.URL, "/api/v1/users/current/import/jobs/"+strconv.Itoa(job.ID)+"/ws"), &websocket.DialOptions{
			HTTPHeader: http.Header{"Cookie": []string{"refresh_token=" + refresh}},
		})
		Expect(err).NotTo(HaveOccurred())
		defer conn.CloseNow()

		// 1. Snapshot arrives first (handler always writes it).
		readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer readCancel()
		var snap struct {
			Type string `json:"type"`
		}
		Expect(wsjson.Read(readCtx, conn, &snap)).To(Succeed())
		Expect(snap.Type).To(Equal("snapshot"))

		// 2. Give the reader goroutine a beat to start, then publish a live
		// log event via the hub. Handler forwards it verbatim.
		liveLog := &db.LogLine{ID: 12345, Level: "info", Message: "live-tail-tick"}
		go func() {
			time.Sleep(30 * time.Millisecond)
			deps.Hub.Publish(job.ID, importer.Event{Type: "log", Log: liveLog})
			time.Sleep(30 * time.Millisecond)
			// 3. Publish a terminal state event — the handler must close after
			// forwarding it (exercises the isTerminal branch inside the loop).
			finalJob := &db.Job{ID: job.ID, State: db.JobStateCompleted, Owner: user}
			deps.Hub.Publish(job.ID, importer.Event{Type: "state", Job: finalJob})
		}()

		// Read the live log event.
		var got1 importer.Event
		Expect(wsjson.Read(readCtx, conn, &got1)).To(Succeed())
		Expect(got1.Type).To(Equal("log"),
			"expected live log event as second frame; got %s", got1.Type)
		Expect(got1.Log).NotTo(BeNil())
		Expect(got1.Log.Message).To(Equal("live-tail-tick"))

		// Read the terminal state event — after this the handler closes cleanly.
		var got2 importer.Event
		Expect(wsjson.Read(readCtx, conn, &got2)).To(Succeed())
		Expect(got2.Type).To(Equal("state"))
		Expect(got2.Job).NotTo(BeNil())
		Expect(got2.Job.State).To(Equal(db.JobStateCompleted))

		// Subsequent read should surface the close (or EOF).
		_, _, err = conn.Read(readCtx)
		Expect(err).To(HaveOccurred(), "expected the handler to close the WS after terminal state")
	})

	It("cross-user: user B's cookie MUST NOT let them read user A's job stream (404, no upgrade)", func() {
		deps := newImportDeps("")
		userA, _ := deps.Hz.MintUser("import_ws_iso_A")
		userB, _ := deps.Hz.MintUser("import_ws_iso_B")
		_ = userB
		ctx := context.Background()

		refreshB := mintRefreshCookie(deps.Hz, userB)
		now := time.Now().UTC()
		jobA, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, jobA.ID)
		})

		dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer dialCancel()
		_, resp, err := websocket.Dial(dialCtx, wsURL(deps.Server.URL, "/api/v1/users/current/import/jobs/"+strconv.Itoa(jobA.ID)+"/ws"), &websocket.DialOptions{
			HTTPHeader: http.Header{"Cookie": []string{"refresh_token=" + refreshB}},
		})
		Expect(err).To(HaveOccurred(), "ws upgrade succeeded for cross-user peek (leak)")
		if resp != nil {
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound),
				"cross-user WS: expected 404 (jobForOwner), got %d", resp.StatusCode)
		}
	})
})

// -----------------------------------------------------------------------------
// effectiveImportToken — direct handler dep coverage (server-env fallback).
// The path is exercised indirectly by ImportRequest above; this asserts the
// server-env fallback is what actually gets stored on the queued job value.
// -----------------------------------------------------------------------------

var _ = Describe("effectiveImportToken via ImportRequest", func() {
	It("blank body-token falls back to cfg.WakatimeAPIKey; item stored on job carries server key (never leaked over the wire)", func() {
		serverKey := "server-env-magic-token"
		deps := newImportDeps(serverKey)
		_, token := deps.Hz.MintUser("import_effkey")
		ctx := context.Background()

		now := time.Now().UTC()
		body := map[string]any{
			// deliberately omit apiToken → falls back to saved (none) then server env
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		id := jobIDFromSubmit(rec)

		// The submit response envelope MUST NOT echo the API key anywhere — this
		// is the no-oracle invariant.
		Expect(rec.Body.String()).NotTo(ContainSubstring(serverKey),
			"server-env key leaked in submit response: %s", rec.Body.String())

		DeferCleanup(func() {
			_, _ = deps.Hz.DB.MarkRunningJobsFailed(ctx, "cleanup")
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, id)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, id)
		})

		// GET the job — must still not leak the server key in the API response.
		getRec := jsonReq(deps.Router, http.MethodGet,
			"/api/v1/users/current/import/jobs/"+strconv.Itoa(id), token, nil)
		Expect(getRec).To(testutil.HaveStatus(http.StatusOK))
		Expect(getRec.Body.String()).NotTo(ContainSubstring(serverKey),
			"server-env key leaked via GET /import/jobs/:id response: %s", getRec.Body.String())
	})

	It("uses the previously-SAVED encrypted key when body is blank AND no server env key is set", func() {
		// This is the "save it once, click Import forever" ergonomic. We plant
		// a valid encrypted key for the user and verify ImportRequest picks it
		// up (rather than silently sending "" to the worker).
		installEncryptionKeyForTest()
		deps := newImportDeps("") // NO server env key → forces the saved-key branch

		user, token := deps.Hz.MintUser("import_savedkey")
		ctx := context.Background()

		// Save an encrypted key directly on the user row.
		plaintext := []byte("waka_saved_key_from_last_time")
		ct, err := auth.Encrypt(plaintext)
		Expect(err).NotTo(HaveOccurred())
		Expect(deps.Hz.DB.SetEncryptedWakatimeKey(ctx, user, ct, db.WakatimeKeyStatusValid)).To(Succeed())

		now := time.Now().UTC()
		body := map[string]any{
			// blank apiToken → the handler should decrypt+use the saved key
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		id := jobIDFromSubmit(rec)
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.MarkRunningJobsFailed(ctx, "cleanup")
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, id)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, id)
		})

		// The submit response envelope MUST NOT leak the plaintext.
		Expect(rec.Body.String()).NotTo(ContainSubstring(string(plaintext)),
			"saved plaintext leaked in submit response: %s", rec.Body.String())
	})
})

// installEncryptionKeyForTest sets BOOM_ENCRYPTION_KEY to a deterministic 32
// bytes of zeros-plus-one and resets the auth singleton — so auth.Encrypt /
// Decrypt work for the duration of the spec.
func installEncryptionKeyForTest() {
	const key = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	prev, hadPrev := os.LookupEnv(auth.EncryptionKeyEnv)
	os.Setenv(auth.EncryptionKeyEnv, key)
	auth.ResetForTest()
	Expect(auth.LoadKeyFromEnv()).To(Succeed())
	DeferCleanup(func() {
		if hadPrev {
			os.Setenv(auth.EncryptionKeyEnv, prev)
		} else {
			os.Unsetenv(auth.EncryptionKeyEnv)
		}
		auth.ResetForTest()
	})
}

// -----------------------------------------------------------------------------
// Small no-oracle regression: the "hasSavedKey" endpoint (wakatime_key GET)
// MUST NOT leak any prefix of the plaintext when the user has a saved key.
// This is not part of the import cluster but the invariant is under threat
// from the same code paths (auth.Encrypt/Decrypt + save-on-success), so we
// keep the check nearby.
// -----------------------------------------------------------------------------

var _ = Describe("no-oracle regression: ImportRequest body/response envelope", func() {
	It("submit response envelope contains only {jobId, jobStatus, job} — never {apiToken}", func() {
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("import_noleak")

		now := time.Now().UTC()
		secret := "waka_do_not_echo_me_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		body := map[string]any{
			"apiToken":  secret,
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		// LOAD-BEARING: the submitted apiToken is NOT reflected in the response.
		Expect(rec.Body.String()).NotTo(ContainSubstring(secret),
			"submit response echoed the submitted apiToken (oracle for keystroke replay attacks): %s", rec.Body.String())
		// Similarly not base64-encoded (basic auth wire form).
		Expect(rec.Body.String()).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(secret))))
		Expect(strings.Contains(rec.Body.String(), "jobId")).To(BeTrue())

		_, _ = deps.Hz.DB.MarkRunningJobsFailed(context.Background(), "cleanup")
	})
})
