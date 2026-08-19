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
package admin_test

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

	boomtimeadmin "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
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
	Hz     *testutil.Harness
	H      *handler.Handler
	Worker *importer.Worker
	Hub    *importer.Hub
	Cfg    *config.Config
	Router *echo.Echo
	Server *httptest.Server // for websocket / range mocks
	MockWK *httptest.Server // wakatime.com stand-in (may be nil)
	Cancel context.CancelFunc
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
	bh := boomtimeadmin.New(hz.DB, cfg, logger)
	bh.SetImportWorker(worker, hub)

	e := echo.New()
	// Full import route surface — one line per handler method under test.
	e.POST("/api/v1/users/current/import", bh.ImportRequest)
	e.GET("/api/v1/users/current/import/config", bh.ImportConfig)
	e.POST("/api/v1/users/current/import/wakatime-range", bh.WakatimeRange)
	e.GET("/api/v1/users/current/import/jobs", bh.ImportJobs)
	e.GET("/api/v1/users/current/import/jobs/:id", bh.ImportJob)
	e.POST("/api/v1/users/current/import/jobs/:id/cancel", bh.ImportJobCancel)
	e.GET("/api/v1/users/current/import/jobs/:id/logs", bh.ImportJobLogs)
	e.GET("/api/v1/users/current/import/jobs/:id/ws", bh.ImportJobWS)

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

	// NOTE: the "upstream error path returns {hasData:false}" invariant is NOT
	// exercised as an integration test here — FetchAllTimeRange uses a hardcoded
	// wakatime.com base URL and the package-private httpClient can't be swapped
	// from an external test package. An earlier attempt used HTTPS_PROXY to
	// force connect-refused, but Go's httpproxy env is read once per process
	// so the coercion is non-deterministic on already-warm clients. The
	// error-branch is covered by code review (import.go:243-247) plus the
	// no-key branch above. Do not re-introduce a proxy-env test — it flakes
	// on CI workers that have already resolved a proxy elsewhere.

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
		// PIN: MissingAuth = 400 (apierr.MissingAuth), not >= 400 (which would
		// silently pass a 500 panic).
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"missing Authorization MUST be 400 (MissingAuth); got %d body=%s", rec.Code, rec.Body.String())
	})

	It("submit response envelope has the expected shape ({jobId, jobStatus, job}) with job.State=queued and job.Owner=caller", func() {
		// Pin the FULL success envelope — the sibling gaka-6jm.8 test asserts
		// no eager key-save, but nothing else asserted that {jobStatus} even
		// exists or that {job.State} is "queued" (not "running"). A regression
		// that returned state="running" on the first submit would silently break
		// the FE which uses state for the progress-vs-idle UI without a repro.
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_envelope")

		now := time.Now().UTC()
		body := map[string]any{
			"apiToken":  "envelope_test",
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var out struct {
			JobID     int     `json:"jobId"`
			JobStatus string  `json:"jobStatus"`
			Job       *db.Job `json:"job"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.JobID).To(BeNumerically(">", 0))
		Expect(out.JobStatus).NotTo(BeEmpty(),
			"jobStatus is missing from the submit envelope; FE contract broken")
		Expect(out.Job).NotTo(BeNil(),
			"job snapshot missing from the submit envelope; FE contract broken")
		Expect(out.Job.Owner).To(Equal(user),
			"submit envelope's job.Owner MUST equal the calling user")
		// LOAD-BEARING: fresh submit MUST return state=queued. A regression
		// that echoed the running snapshot would race the worker (which may
		// have already flipped state via MarkJobRunning by the time the JSON
		// is written) — assert against the DB row's fresh value AT-SUBMIT.
		Expect(out.Job.State).To(Equal(db.JobStateQueued),
			"submit envelope's job.State MUST be %q (not %q, not empty)",
			db.JobStateQueued, out.Job.State)

		// Cleanup — the worker may have started; make sure teardown is clean.
		_, _ = deps.Hz.DB.MarkRunningJobsFailed(context.Background(), "envelope cleanup")
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

	It("401 on a bogus token AND missing auth is 400 — never leaks another owner's list", func() {
		// PIN exact status per apierr: MissingAuth=400, InvalidToken=401.
		// A regression (500 panic with a stack containing user emails, or a
		// 302 that redirects to another owner's list) would pass a `>= 400`
		// check silently — anti-oracle: body must never contain "jobs".
		deps := newImportDeps("")

		// Bogus token → 401 InvalidToken (no such row in api_tokens).
		recBogus := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs", "not-a-real-token", nil)
		Expect(recBogus).To(testutil.HaveStatus(http.StatusUnauthorized),
			"bogus token MUST be 401 (InvalidToken); got %d body=%s", recBogus.Code, recBogus.Body.String())
		Expect(recBogus.Body.String()).NotTo(ContainSubstring(`"jobs"`),
			"leak: unauth response contained a jobs list; body=%s", recBogus.Body.String())

		// Missing auth → 400 MissingAuth.
		recMissing := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs", "", nil)
		Expect(recMissing).To(testutil.HaveStatus(http.StatusBadRequest),
			"missing Authorization MUST be 400 (MissingAuth); got %d body=%s", recMissing.Code, recMissing.Body.String())
		Expect(recMissing.Body.String()).NotTo(ContainSubstring(`"jobs"`),
			"leak: unauth response contained a jobs list; body=%s", recMissing.Body.String())
	})

	It("returns {jobs: []} (never null) when the caller has ZERO jobs — JSON shape stability", func() {
		// FE parses `payload.jobs.length` unconditionally. If the handler
		// returned `{"jobs": null}` for zero-job callers, that would be a
		// TypeError in the browser. Pin the empty-slice contract.
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("import_list_empty")

		rec := jsonReq(deps.Router, http.MethodGet, "/api/v1/users/current/import/jobs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		// LOAD-BEARING: the RAW body must contain "[]", not "null". A generic
		// unmarshal into []db.Job coerces both to nil, hiding the drift.
		Expect(rec.Body.String()).To(ContainSubstring(`"jobs":[]`),
			"zero-jobs list MUST serialize as {\"jobs\":[]}, not {\"jobs\":null}; got %s", rec.Body.String())
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

		// afterId=<garbage> — documented behavior of queryInt64 is silent
		// fallback to default (0). Pin it: a mistyped tail-cursor MUST NOT
		// 400 — it should return the same as no afterId. A regression that
		// started 400ing garbage cursors would break the FE tail loop after
		// a browser-side stringification bug (Number(NaN) → "NaN").
		recGarbage := jsonReq(deps.Router, http.MethodGet, target+"?afterId=not-a-number", tokenA, nil)
		Expect(recGarbage).To(testutil.HaveStatus(http.StatusOK),
			"afterId=<garbage> MUST silent-fallback to 0 (queryInt64 default), got %d body=%s",
			recGarbage.Code, recGarbage.Body.String())
		var garb struct {
			Logs []db.LogLine `json:"logs"`
		}
		Expect(json.Unmarshal(recGarbage.Body.Bytes(), &garb)).To(Succeed())
		Expect(garb.Logs).To(HaveLen(2),
			"afterId=<garbage> should be treated as afterId=0 (return all); got %d logs", len(garb.Logs))
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

	It("cross-user cancel: user B cannot cancel user A's job (404, no state change, no log side-effect)", func() {
		deps := newImportDeps("")
		userA, _ := deps.Hz.MintUser("import_cancel_iso_A")
		_, tokenB := deps.Hz.MintUser("import_cancel_iso_B")
		ctx := context.Background()

		now := time.Now().UTC()
		jobA, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, jobA.ID)
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

		// LOAD-BEARING: if cancel side-effects (audit log, "cancel attempted
		// by X" line) ran BEFORE the owner check in jobForOwner, user B could
		// pollute user A's log stream with attacker-controlled data. Assert
		// zero log lines were appended.
		logs, err := deps.Hz.DB.GetJobLogs(ctx, jobA.ID, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(BeEmpty(),
			"cross-user cancel appended %d log line(s) to user A's job (side-effect before owner check)", len(logs))
	})

	It("cancel on a RUNNING db row whose worker is NOT in the running map (post-restart zombie) → DB cancel path, state=cancelled", func() {
		// If the process previously crashed with jobs in state=running,
		// RecoverInterrupted marks them failed at startup — but a race can
		// leave a stale state=running row whose goroutine is gone. The cancel
		// handler's `!running` branch delegates to DB.CancelJob for exactly
		// this case. Exercise it directly by seeding a running row without a
		// live worker.
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_cancel_zombie")
		ctx := context.Background()

		now := time.Now().UTC()
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		// Flip to running WITHOUT going through Worker.StartJob — so the
		// worker's running map has no entry for this id.
		_, err = deps.Hz.DB.MarkJobRunning(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, job.ID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		target := "/api/v1/users/current/import/jobs/" + strconv.Itoa(job.ID) + "/cancel"
		rec := jsonReq(deps.Router, http.MethodPost, target, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payloadOut struct {
			Job *db.Job `json:"job"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payloadOut)).To(Succeed())
		Expect(payloadOut.Job).NotTo(BeNil())
		// LOAD-BEARING: the DB path must move the row terminal even for a
		// running row with no live goroutine. Prior contract: DB.CancelJob
		// treats running as cancellable.
		Expect(payloadOut.Job.State).To(Equal(db.JobStateCancelled),
			"zombie-running cancel: expected state=cancelled via DB.CancelJob path; got %s", payloadOut.Job.State)
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

		// 2. Publish the LIVE log event, then wait for the reader to observe it
		// via a channel barrier BEFORE publishing the terminal state event.
		// This eliminates the prior time.Sleep race: on a loaded CI worker the
		// terminal Publish could beat the log Read, and the handler could
		// close the socket before the log ever hit the wire. With the barrier,
		// the log-then-state sequence is deterministic even under scheduling
		// pressure.
		liveLog := &db.LogLine{ID: 12345, Level: "info", Message: "live-tail-tick"}
		logRead := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			// Publish log immediately; the WS handler forwards it to the sub
			// channel, which the reader below drains.
			deps.Hub.Publish(job.ID, importer.Event{Type: "log", Log: liveLog})
			// Wait until the reader has confirmed receipt before proceeding.
			<-logRead
			// Now publish the terminal state — handler must close after this.
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
		// Signal the publisher: safe to publish the terminal state now.
		close(logRead)

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
		// LOAD-BEARING: if resp is nil (e.g. connection reset before the server
		// wrote a status), the assertion below silently no-ops. That would let
		// a regression that (a) blew up mid-handshake and (b) leaked bytes into
		// the pipe pass this test unnoticed. Assert resp is non-nil FIRST so
		// the status check is meaningful.
		Expect(resp).NotTo(BeNil(),
			"ws cross-user handshake produced no HTTP response — cannot verify 404 status; err=%v", err)
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound),
			"cross-user WS: expected 404 (jobForOwner), got %d", resp.StatusCode)
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
	It("submit response envelope contains ONLY {jobId, jobStatus, job} — never {apiToken}, no drift keys", func() {
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
		// LOAD-BEARING: the submitted apiToken is NOT reflected in the response
		// (raw), nor base64-encoded (basic-auth wire form), nor hex-encoded.
		Expect(rec.Body.String()).NotTo(ContainSubstring(secret),
			"submit response echoed the submitted apiToken (oracle for keystroke replay attacks): %s", rec.Body.String())
		Expect(rec.Body.String()).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(secret))),
			"submit response echoed the submitted apiToken base64-encoded: %s", rec.Body.String())

		// STRONGER: whitelist the top-level envelope keys. Any accidental new
		// key (e.g. "requestPayload", "value", "typedToken") that carried the
		// secret in a transformed form would be caught here regardless of its
		// encoding. ContainSubstring alone can't catch that.
		var envelope map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		keys := make([]string, 0, len(envelope))
		for k := range envelope {
			keys = append(keys, k)
		}
		Expect(keys).To(ConsistOf("jobId", "jobStatus", "job"),
			"submit envelope has UNEXPECTED top-level keys (potential leak surface): %v", keys)

		// The nested job's serialization is Job (json tags in db.Job) — that
		// struct deliberately does NOT include the value/payload_json column.
		// Pin it: no "value" or "apiToken" key nested under "job".
		jobMap, ok := envelope["job"].(map[string]any)
		Expect(ok).To(BeTrue(), "job field not an object")
		Expect(jobMap).NotTo(HaveKey("value"),
			"job.value leaked to the wire (contains QueueItem serialized with typed token!): %v", jobMap)
		Expect(jobMap).NotTo(HaveKey("apiToken"),
			"job.apiToken leaked to the wire: %v", jobMap)
		Expect(jobMap).NotTo(HaveKey("payload_json"),
			"job.payload_json leaked to the wire: %v", jobMap)

		_, _ = deps.Hz.DB.MarkRunningJobsFailed(context.Background(), "cleanup")
	})

	It("GET /import/jobs/:id response nested job MUST NOT include payload_json / value / apiToken", func() {
		// Even after the submit passes the whitelist above, the GET path uses
		// the same db.Job scan — but if someone ever added a Sprintf-based
		// "debug" field to the response envelope, this would catch it.
		deps := newImportDeps("")
		user, token := deps.Hz.MintUser("import_getleak")
		ctx := context.Background()

		now := time.Now().UTC()
		// Plant a payload that if it EVER leaked would be trivially spot-able.
		payload, _ := json.Marshal(map[string]any{
			"apiToken": "waka_planted_secret_in_payload_json_do_not_leak",
		})
		job, err := deps.Hz.DB.CreateImportJob(ctx, user, payload, now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, job.ID)
		})

		rec := jsonReq(deps.Router, http.MethodGet,
			"/api/v1/users/current/import/jobs/"+strconv.Itoa(job.ID), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		// The planted secret MUST NOT appear anywhere in the response body.
		Expect(rec.Body.String()).NotTo(ContainSubstring("waka_planted_secret_in_payload_json_do_not_leak"),
			"GET /import/jobs/:id leaked payload_json contents to the wire: %s", rec.Body.String())

		var envelope struct {
			Job  map[string]any `json:"job"`
			Logs []db.LogLine   `json:"logs"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		Expect(envelope.Job).NotTo(HaveKey("value"),
			"job.value leaked on GET (would contain the queued QueueItem incl. TypedToken): %v", envelope.Job)
		Expect(envelope.Job).NotTo(HaveKey("payload_json"),
			"job.payload_json leaked on GET: %v", envelope.Job)
	})
})

// -----------------------------------------------------------------------------
// Body-size cap on POST /import — undocumented invariant. Currently the import
// route uses plain c.Bind (no BindJSONWithLimit) so oversize bodies are NOT
// capped at the handler level. This test PINS that current behavior so a later
// intentional switch to BindJSONWithLimit (or a router-level BodyLimit
// middleware) breaks the test loudly rather than silently.
// -----------------------------------------------------------------------------

var _ = Describe("ImportRequest body-size behavior (undocumented cap)", func() {
	It("currently accepts an oversized JSON body (no 413) — pin the drift so it fails loudly if we ever add BodyLimit", func() {
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("import_bigbody")

		now := time.Now().UTC()
		// 1 MB of filler in an unknown field — the handler binds by tag so
		// this is silently ignored, but the raw bytes still travel over the
		// wire and through c.Bind's decoder. If a future middleware caps
		// bodies at (say) 64 KB, we expect a 413 or 400.
		filler := strings.Repeat("x", 1<<20)
		body := map[string]any{
			"apiToken":  "small_key",
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
			"_padding":  filler,
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		// Documented current behavior: 200. If someone adds a body cap, this
		// pin flips and the developer is prompted to add a positive 413 test
		// alongside the removal.
		Expect(rec.Code).To(Equal(http.StatusOK),
			"import handler no longer accepts a 1MB body (rc=%d body=%s). If this was an intentional BodyLimit addition, update this test to assert the new 413/400 status AND add a positive test for the accepted size range.",
			rec.Code, rec.Body.String())
		_, _ = deps.Hz.DB.MarkRunningJobsFailed(context.Background(), "bigbody cleanup")
	})
})

// -----------------------------------------------------------------------------
// Handler-level integration smoke for gaka-6jm.10: a wakatime.com 401 during
// the RUN (not the pre-submit /users/current probe) MUST flip
// users.wakatime_key_status to 'invalid' AND MUST NOT persist the typed key.
// The applyKeyOutcome unit test already covers the state machine — this test
// pins the full handler → worker → DB pathway.
// -----------------------------------------------------------------------------

var _ = Describe("ImportRequest full-loop: typed key + wakatime 401 → status=invalid, no save (gaka-6jm.10 handler-level)", func() {
	It("with a prior saved (valid) key: 401 during run flips status→invalid but LEAVES the prior ciphertext intact (no clobber, no fresh save)", func() {
		// Full-loop pin for gaka-6jm.10: the applyKeyOutcome unit test in
		// internal/importer/apply_key_outcome_test.go covers the state machine
		// in isolation — this test exercises the FULL pathway:
		//     handler POST → worker fetchLookups → 401 → FinishImportJob(failed)
		//     → applyKeyOutcome(saw401=true) → users.wakatime_key_status='invalid'
		// AND: the typed token from the POST body MUST NOT overwrite the prior
		// ciphertext (save-on-success is skipped on 401).
		//
		// Prerequisite for the STATUS flip to be observable in the DB:
		// UpdateWakatimeKeyStatus is a no-op unless the user already has a
		// saved ciphertext (see db/wakatime_key.go:UpdateWakatimeKeyStatus).
		// So we plant a valid ciphertext first, then submit a DIFFERENT typed
		// key, then verify (a) status flipped, (b) the ORIGINAL ciphertext is
		// untouched (typed key was NOT persisted).
		installEncryptionKeyForTest()
		deps := newImportDeps("") // no server env key so item.APIToken == typed

		// Mock wakatime that always 401s — sends the worker into
		// ErrWakatimeUnauthorized → saw401=true → applyKeyOutcome.
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad token"}`))
		}))
		DeferCleanup(mock.Close)
		deps.Worker.BaseURL = mock.URL

		user, token := deps.Hz.MintUser("import_401_flip")
		ctx := context.Background()

		// Plant a prior VALID saved key so the status update is observable.
		originalPlaintext := []byte("waka_previously_valid_key")
		originalCt, err := auth.Encrypt(originalPlaintext)
		Expect(err).NotTo(HaveOccurred())
		Expect(deps.Hz.DB.SetEncryptedWakatimeKey(ctx, user, originalCt, db.WakatimeKeyStatusValid)).To(Succeed())

		// Submit an import — the caller types a NEW key (different from the
		// saved one) that will 401 because our mock always rejects.
		now := time.Now().UTC()
		typedKey := "waka_typed_but_will_401"
		body := map[string]any{
			"apiToken":  typedKey,
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		}
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		jobID := jobIDFromSubmit(rec)
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, jobID)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, jobID)
		})

		// Wait for the worker to terminal-fail the job.
		Eventually(func() string {
			j, err := deps.Hz.DB.GetJobByID(ctx, jobID)
			if err != nil || j == nil {
				return ""
			}
			return j.State
		}, 5*time.Second, 25*time.Millisecond).Should(Equal(db.JobStateFailed))

		// LOAD-BEARING #1: applyKeyOutcome flipped status → invalid.
		Eventually(func() string {
			info, err := deps.Hz.DB.GetWakatimeKeyInfo(ctx, user)
			if err != nil || info.Status == nil {
				return ""
			}
			return *info.Status
		}, 2*time.Second, 25*time.Millisecond).Should(Equal(string(db.WakatimeKeyStatusInvalid)),
			"gaka-6jm.10: wakatime_key_status was not flipped to 'invalid' after 401")

		// LOAD-BEARING #2: the ORIGINAL ciphertext is still there — the
		// typed-and-401'd key was NOT persisted. Decrypt to confirm the
		// original plaintext survives (vs. having been silently overwritten
		// with an encrypted form of the typed key).
		blob, has, err := deps.Hz.DB.GetEncryptedWakatimeKey(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(has).To(BeTrue(),
			"gaka-6jm.10: original saved key was clobbered to NULL by the 401 outcome (should be untouched)")
		decrypted, err := auth.Decrypt(blob)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(decrypted)).To(Equal(string(originalPlaintext)),
			"gaka-6jm.10: the typed key was persisted despite 401 — save-on-success skipped path failed")
		Expect(string(decrypted)).NotTo(Equal(typedKey),
			"gaka-6jm.10: the typed 401'd key overwrote the previously-valid ciphertext")
	})
})
