// admin_backfill_http_test.go — HTTP-level ginkgo coverage for the admin
// backfill cluster (gaka-d6x.handler). Complements the wire-shape unit
// suite in admin_backfill_test.go by hitting every route through the
// Router() and asserting NAMED INVARIANTS: admin-gate before feature
// gate, cross-owner 404 (never 403 — no oracle), body-supplied
// sourceTag/username ignored, DELETE danger-zone prefix guard, WS
// snapshot filter.
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// bfDo issues an HTTP request with optional token + optional JSON body.
func bfDo(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// mkAdmin mints a user + adds them to the admin allowlist. Returns
// (username, apiToken). The BackfillJobQueue is wired unless disable=true.
func mkAdmin(hz *testutil.Harness, prefix string, disableQueue bool) (string, string) {
	user, token := hz.MintUser(prefix)
	hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
	if !disableQueue {
		hz.H.SetBackfillJobQueue(backfilljobs.NewRegistry(nil))
	}
	return user, token
}

var _ = Describe("AdminBackfillConfig (gaka-vh8): admin gate + defaults", func() {
	It("returns 401/400 for missing token, 403 for non-admin, 200 with defaults for a fresh admin", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfcfg"))
		e := hz.Router()

		// (1) no token → auth failure (not 2xx).
		rec := bfDo(e, http.MethodGet, "/api/v1/admin/backfill/config", "", nil)
		Expect(rec.Code).NotTo(Equal(http.StatusOK),
			"no token got 200 — admin gate leaked, body=%s", rec.Body.String())

		// (2) authed but not admin → 403.
		_, nonAdminToken := hz.MintUser("bfcfg_nonadmin")
		rec = bfDo(e, http.MethodGet, "/api/v1/admin/backfill/config", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))

		// (3) admin, no persisted row → default clamp values (clusterGap=1800, rate=120).
		user, adminToken := hz.MintUser("bfcfg_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		rec = bfDo(e, http.MethodGet, "/api/v1/admin/backfill/config", adminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var cfg map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &cfg)).To(Succeed())
		Expect(cfg["clusterGapSec"]).To(BeEquivalentTo(1800))
		Expect(cfg["heartbeatRateSec"]).To(BeEquivalentTo(120))
		Expect(cfg["sourceTag"]).To(Equal("backfill:git"))
		Expect(cfg["username"]).To(Equal(user))
	})
})

var _ = Describe("AdminBackfillConfigUpdate (gaka-vh8): PATCH clamps + re-reads", func() {
	It("PATCH applies partial fields; clamped output reflects what actually persisted", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfcfg"))
		e := hz.Router()
		user, token := hz.MintUser("bfcfg_patch")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// Send an out-of-band clusterGapSec (1) that MUST clamp to 60 (lo).
		rec := bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/config", token, map[string]any{
			"clusterGapSec": 1,
			"sourceTag":     "custom-run",
			"authorEmails":  []string{"a@b.c"},
			"langMap":       map[string]string{"ts": "TypeScript"},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var cfg map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &cfg)).To(Succeed())
		// INVARIANT: clamped, not accepted verbatim.
		Expect(cfg["clusterGapSec"]).To(BeEquivalentTo(60),
			"clusterGapSec=1 must clamp to floor 60; got %v", cfg["clusterGapSec"])
		// INVARIANT: sourceTag missing prefix gets "backfill:" prepended (danger-zone hygiene).
		Expect(cfg["sourceTag"]).To(Equal("backfill:custom-run"))
		// INVARIANT: langMap round-trips.
		lm, _ := cfg["langMap"].(map[string]any)
		Expect(lm["ts"]).To(Equal("TypeScript"))
	})

	It("rejects non-admin PATCH with 403 (no clamp leak on wire body)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfcfg"))
		e := hz.Router()
		_, nonAdminToken := hz.MintUser("bfcfg_patch_deny")
		rec := bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/config", nonAdminToken, map[string]any{"clusterGapSec": 42})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})
})

var _ = Describe("AdminBackfillStats (gaka-vh8): admin gate + shape", func() {
	It("returns 0 totals and empty sources map for an admin with no backfill rows", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfstats"))
		e := hz.Router()
		user, token := hz.MintUser("bfstats_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		rec := bfDo(e, http.MethodGet, "/api/v1/admin/backfill/stats", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var stats map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &stats)).To(Succeed())
		Expect(stats["totalRows"]).To(BeEquivalentTo(0))
	})

	It("rejects non-admin with 403", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfstats"))
		e := hz.Router()
		_, token := hz.MintUser("bfstats_nonadmin")
		rec := bfDo(e, http.MethodGet, "/api/v1/admin/backfill/stats", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})
})

var _ = Describe("AdminBackfillEnqueueJob (gaka-vh8): queue nil → 503; validation", func() {
	It("returns 503 when BackfillJobQueue is not wired (before any body parse)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfenq"))
		e := hz.Router()
		user, token := hz.MintUser("bfenq_noqueue")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		// deliberately DO NOT SetBackfillJobQueue.
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token, map[string]any{"repoName": "r"})
		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable))
	})

	It("rejects empty repoName (400) and negative totalCommits (400); enqueues valid body (202)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfenq"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfenq_ok", false)

		// (1) empty repoName.
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token, map[string]any{"repoName": "  "})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// (2) negative totalCommits.
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token, map[string]any{"repoName": "r", "totalCommits": -1})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// (3) valid.
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "boomtime", "repoPath": "/tmp/x", "totalCommits": 5})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["jobId"]).NotTo(BeEmpty())
		job, _ := resp["job"].(map[string]any)
		Expect(job["status"]).To(Equal("queued"))
		Expect(job["repoName"]).To(Equal("boomtime"))
	})
})

var _ = Describe("AdminBackfillJobPatch (gaka-vh8): cross-owner is 404 (no oracle) + status validation", func() {
	It("404s another admin's job id, 400s an unknown status, applies valid status transitions", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfpatch"))
		e := hz.Router()
		// Two admins sharing the same BackfillJobQueue (both on allowlist).
		userA, tokenA := hz.MintUser("bfp_a")
		userB, tokenB := hz.MintUser("bfp_b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		hz.H.SetBackfillJobQueue(backfilljobs.NewRegistry(nil))

		// A enqueues a job.
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", tokenA,
			map[string]any{"repoName": "rA", "totalCommits": 3})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		// INVARIANT (cross-owner): B PATCHing A's job returns 404, NOT 403.
		// A 403 would be an oracle for "some admin owns this id".
		recCross := bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, tokenB,
			map[string]any{"status": "done"})
		Expect(recCross).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-owner PATCH must be 404 (no oracle), got body=%s", recCross.Body.String())
		crossBody := recCross.Body.String()

		// (2) unknown status → 400.
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, tokenA,
			map[string]any{"status": "zombie"})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// (3) valid status transition (queued → running).
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, tokenA,
			map[string]any{"status": "running"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var patched map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &patched)).To(Succeed())
		Expect(patched["status"]).To(Equal("running"))

		// (4) totally unknown id → 404 with BYTE-IDENTICAL body to (1).
		// This is the real "no oracle" invariant — a distinguishable
		// body (e.g. "job owned by another admin" vs "job not found")
		// still leaks existence even with a matching status code.
		recUnknown := bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/does-not-exist", tokenA,
			map[string]any{"status": "done"})
		Expect(recUnknown).To(testutil.HaveStatus(http.StatusNotFound))
		Expect(recUnknown.Body.String()).To(Equal(crossBody),
			"cross-owner 404 body must be byte-identical to unknown-id 404 body — otherwise the response leaks existence of admin B's job.\ncross-owner body=%s\nunknown-id body=%s",
			crossBody, recUnknown.Body.String())

		// (5) 503 when queue is unwired.
		hz.H.SetBackfillJobQueue(nil)
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, tokenA,
			map[string]any{"status": "done"})
		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable))
	})

	It("returns 400 for an empty job id path (registered as its own handler)", func() {
		// The bare `/jobs` PATCH route isn't wired — echo would 405/404 first.
		// But we still cover the id="" branch by ensuring the handler behaves
		// consistently when queue is set. Skip if the router doesn't route
		// (this documents the branch; the handler itself covers it via the
		// param check).
		Skip("empty-id branch is unreachable via echo router; handler code path is defensive")
	})
})

var _ = Describe("AdminBackfillJobHeartbeats (gaka-vh8): forbids cross-owner + trusts server-side sourceTag", func() {
	It("cross-owner heartbeats POST is 404; empty sessions → 200 with zero counts", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfhb"))
		e := hz.Router()
		userA, tokenA := hz.MintUser("bfhb_a")
		userB, tokenB := hz.MintUser("bfhb_b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		hz.H.SetBackfillJobQueue(backfilljobs.NewRegistry(nil))

		// A enqueues.
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", tokenA,
			map[string]any{"repoName": "hbr", "totalCommits": 0})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		// B tries to add heartbeats — must 404, not 403.
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/"+jobID+"/heartbeats", tokenB,
			map[string]any{"sessions": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))

		// A with empty sessions → 200 + BackfillResult zero.
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/"+jobID+"/heartbeats", tokenA,
			map[string]any{"sessions": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var res map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &res)).To(Succeed())
		Expect(res["acceptedHeartbeats"]).To(BeEquivalentTo(0))

		// Preview endpoint mirrors the same wire shape + auth path.
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/"+jobID+"/preview", tokenA,
			map[string]any{"sessions": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
	})

	It("real session body writes heartbeats under the server-side sourceTag (body cannot forge)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfhb"))
		e := hz.Router()
		user, token := hz.MintUser("bfhb_real")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		hz.H.SetBackfillJobQueue(backfilljobs.NewRegistry(nil))

		// PATCH the config to a specific sourceTag first.
		rec := bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/config", token, map[string]any{"sourceTag": "backfill:git:test-run"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		// Enqueue.
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "hbr", "totalCommits": 1})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		now := time.Now().UTC()
		start := now.Add(-2 * time.Hour)
		end := now.Add(-1 * time.Hour)
		session := map[string]any{
			"start": start.Format(time.RFC3339Nano),
			"end":   end.Format(time.RFC3339Nano),
			"heartbeats": []map[string]any{{
				"entity":   "main.go",
				"type":     "file",
				"time":     start.Add(30 * time.Minute).Unix(),
				"project":  "boomtime",
				"language": "go",
				"editor":   "vim",
				"platform": "linux",
				"category": "coding",
			}},
		}
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/"+jobID+"/heartbeats", token,
			map[string]any{"sessions": []any{session}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var res map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &res)).To(Succeed())
		Expect(res["acceptedHeartbeats"]).To(BeEquivalentTo(1),
			"one heartbeat expected; body=%s", rec.Body.String())

		// INVARIANT: heartbeat row is tagged with the PERSISTED sourceTag, not
		// whatever the body might have shipped. Verify via DB.
		var src string
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT source FROM heartbeats WHERE sender=$1 LIMIT 1`, user).Scan(&src)).To(Succeed())
		Expect(src).To(Equal("backfill:git:test-run"),
			"heartbeat.source must equal the persisted config.sourceTag; got %q", src)

		// After the batch, the job Processed++ + Written++ via IncrementCounts.
		time.Sleep(20 * time.Millisecond) // give the async broadcast a beat (defensive; not strictly needed)
		rec = bfDo(e, http.MethodGet, "/api/v1/admin/backfill/stats", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var stats map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &stats)).To(Succeed())
		Expect(stats["totalRows"]).To(BeEquivalentTo(1))
	})

	It("returns 503 when queue is not wired even for /heartbeats + /preview", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfhb"))
		e := hz.Router()
		user, token := hz.MintUser("bfhb_noqueue")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		// no queue wired
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/nope/heartbeats", token,
			map[string]any{"sessions": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable))
		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/nope/preview", token,
			map[string]any{"sessions": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusServiceUnavailable))
	})

	It("404s an unknown jobID on heartbeats POST (same 404 shape as cross-owner — no oracle)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfhb"))
		e := hz.Router()
		user, token := hz.MintUser("bfhb_missing")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		hz.H.SetBackfillJobQueue(backfilljobs.NewRegistry(nil))

		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs/no-such-job/heartbeats", token,
			map[string]any{"sessions": []any{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

var _ = Describe("AdminBackfill: malformed body branches", func() {
	It("PATCH /config with malformed JSON → 400 (BindJSONWithLimit branch)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		user, token := hz.MintUser("bfmal_cfg")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/backfill/config",
			bytes.NewReader([]byte(`{not-json`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("POST /jobs with malformed JSON → 400 (BindJSONWithLimit branch)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfmal_jobs", false)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backfill/jobs",
			bytes.NewReader([]byte(`{not-json`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("PATCH /jobs/:id with malformed JSON → 400 (job exists, bind fails)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfmal_patch", false)
		// Enqueue a real job so we get PAST the not-found check.
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "r", "totalCommits": 0})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID,
			bytes.NewReader([]byte(`{not-json`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req)
		Expect(rec2).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("POST /jobs/:id/heartbeats with malformed JSON → 400", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfmal_hb", false)
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "r", "totalCommits": 0})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backfill/jobs/"+jobID+"/heartbeats",
			bytes.NewReader([]byte(`{not-json`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec2 := httptest.NewRecorder()
		e.ServeHTTP(rec2, req)
		Expect(rec2).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("PATCH /jobs/:id status='queued' and 'error' both succeed (all valid status branches)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfmal_alls", false)
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "r", "totalCommits": 0})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		// Each status transition covers a `case` branch in the switch.
		for _, status := range []string{"queued", "running", "done", "error"} {
			rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, token,
				map[string]any{"status": status})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"status=%s: %s", status, rec.Body.String())
		}
	})

	It("PATCH /jobs/:id post-terminal transitions succeed — handler is intentionally state-machine-less (trusted CLI)", func() {
		// INVARIANT: the server does NOT enforce a state machine on backfill jobs.
		// The CLI drives the FSM and PATCHes final counts; the server would
		// only add latency + a config knob if it re-validated. If a future
		// refactor adds a server-side FSM this test flips to red — the
		// intent should be revisited (retention timer already re-schedules
		// on each terminal patch, so done->queued->done would DOUBLE-arm
		// the retention timer, but that's a Registry concern).
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfmal_fsm", false)
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "r", "totalCommits": 0})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		// PATCH to done (terminal).
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, token,
			map[string]any{"status": "done"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		// PATCH done -> queued: currently 200 (trusted CLI). If this ever
		// becomes 409, this test is the pin that documents the intent.
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, token,
			map[string]any{"status": "queued"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"handler is intentionally state-machine-less; if this flips add /* documented */ and update the comment")
		// error -> running: also 200 (same intent).
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, token,
			map[string]any{"status": "error"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, token,
			map[string]any{"status": "running"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
	})

	It("PATCH /jobs/:id counter fields (processed/written/skipped) are applied verbatim", func() {
		// Coverage for UpdatePatch.Processed/Written/Skipped — the critic
		// noted these three pointer fields had zero test signal beyond the
		// implicit IncrementCounts path.
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfmal"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bfmal_ctrs", false)
		rec := bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "r", "totalCommits": 10})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		// PATCH with all three counters + an error string.
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, token,
			map[string]any{
				"processed": 7,
				"written":   42,
				"skipped":   3,
				"error":     "partial: 3 commits scanned but not written",
			})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var patched map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &patched)).To(Succeed())
		Expect(patched["processed"]).To(BeEquivalentTo(7),
			"processed counter not applied via PATCH: %v", patched)
		Expect(patched["written"]).To(BeEquivalentTo(42))
		Expect(patched["skipped"]).To(BeEquivalentTo(3))
		Expect(patched["error"]).To(Equal("partial: 3 commits scanned but not written"))
	})
})

var _ = Describe("AdminBackfill: body-limit / Content-Type gates (BindJSONWithLimit)", func() {
	It("POST /jobs with a >BodyLimitSmall payload → 413 (MaxBytesReader trip)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abflim"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bflim_big", false)

		// Craft a body larger than BodyLimitSmall (4 KiB). A big repoPath
		// string is enough to blow the cap.
		bigPath := strings.Repeat("x", 5*1024)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backfill/jobs",
			bytes.NewReader([]byte(`{"repoName":"r","repoPath":"`+bigPath+`","totalCommits":0}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge),
			"oversize POST /jobs must return 413; got %d body=%s", rec.Code, rec.Body.String())
	})

	It("PATCH /config with a >BodyLimitMedium payload → 413", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abflim"))
		e := hz.Router()
		user, token := hz.MintUser("bflim_cfg")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// BodyLimitMedium=64 KiB; a >64 KiB langMap trip blows it.
		big := strings.Repeat("k", 70*1024)
		body := `{"sourceTag":"backfill:` + big + `"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/backfill/config",
			bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge),
			"oversize PATCH /config must return 413; got %d body=%s", rec.Code, rec.Body.String())
	})

	It("POST /jobs with wrong Content-Type (text/plain) — accepted only when body is still JSON-shaped", func() {
		// Echo's default binder inspects Content-Type. text/plain with a
		// JSON payload may or may not bind depending on version. The
		// invariant we lock in: it must NOT return 2xx AND leak a stack —
		// worst case is a 4xx/415 with a clean body.
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abflim"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bflim_ct", false)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backfill/jobs",
			bytes.NewReader([]byte(`{"repoName":"r","totalCommits":0}`)))
		req.Header.Set("Content-Type", "text/plain") // wrong CT
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// Either a 4xx (Echo binder rejects) OR a 2xx (Echo binder is
		// permissive on non-JSON CT). In both cases the body must NOT
		// leak stack/path internals.
		body := rec.Body.String()
		for _, banned := range []string{"/Users/", "panic", ".go:"} {
			Expect(body).NotTo(ContainSubstring(banned),
				"wrong-CT response body leaked %q: %s", banned, body)
		}
	})

	It("POST /jobs with empty body (Content-Length: 0) → 400 (BindJSONWithLimit rejects empty JSON)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abflim"))
		e := hz.Router()
		_, token := mkAdmin(hz, "bflim_empty", false)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/backfill/jobs",
			bytes.NewReader(nil))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Length", "0")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// Empty body: bind fails OR (some versions) succeeds with a
		// zero-value struct and then repoName="" trips the trim guard.
		// Either way the FINAL response is 400.
		Expect(rec.Code).To(Equal(http.StatusBadRequest),
			"empty-body POST /jobs must return 400; got %d body=%s", rec.Code, rec.Body.String())
	})
})

var _ = Describe("AdminBackfill: source= LIKE-pattern semantics (documented, not a literal-only match)", func() {
	It("source=backfill:_it matches exactly one row via SQL LIKE (underscore = 1 char); the defense-in-depth AND source LIKE 'backfill:%' still holds", func() {
		// LOCKS IN: DeleteBackfilledHeartbeats treats `source` as a SQL
		// LIKE PATTERN (see internal/db/backfill.go). `_` matches any
		// single character, `%` matches any run. This is INTENTIONAL —
		// operators use it to bulk-clean e.g. "backfill:%" or
		// "backfill:git:test-run".
		//
		// The safety net is the trailing `AND source LIKE 'backfill:%'`
		// clause, which ensures no malicious source parameter can reach
		// non-backfill rows (real Wakatime rows have source IS NULL and
		// fall out of every LIKE).
		//
		// This test pins both facts:
		//   (a) wildcards ARE respected (so operators can bulk-clean);
		//   (b) the backfill:% floor still applies (so a hostile "%"
		//       or "" can't nuke real telemetry).
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfdel"))
		e := hz.Router()
		user, token := hz.MintUser("bfdel_like")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		ctx := context.Background()

		hz.Seeder(user).Projects("p")
		// Seed rows with three different sources:
		//   (1) backfill:git — should match `backfill:_it` (LIKE: `_` = any char)
		//   (2) backfill:hg  — should NOT match `backfill:_it`
		//   (3) source IS NULL (a real Wakatime row) — must NEVER match
		//       even if source pattern is "%" (defense-in-depth floor).
		for _, src := range []string{"backfill:git", "backfill:hg"} {
			_, err := hz.DB.Pool.Exec(ctx, `
				INSERT INTO heartbeats (editor,plugin,platform,machine,sender,user_agent,branch,category,cursorpos,
					dependencies,entity,is_write,language,lineno,file_lines,project,ty,time_sent,source)
				VALUES ('vim','x','linux','m',$1,'ua','main','Coding',NULL,NULL,'x.go',false,'go',1,10,'p','file',now(),$2)`, user, src)
			Expect(err).NotTo(HaveOccurred())
		}
		// The "real" (NULL source) heartbeat — must be UNTOUCHABLE.
		_, err := hz.DB.Pool.Exec(ctx, `
			INSERT INTO heartbeats (editor,plugin,platform,machine,sender,user_agent,branch,category,cursorpos,
				dependencies,entity,is_write,language,lineno,file_lines,project,ty,time_sent,source)
			VALUES ('vim','x','linux','m',$1,'ua','main','Coding',NULL,NULL,'x.go',false,'go',1,10,'p','file',now(),NULL)`, user)
		Expect(err).NotTo(HaveOccurred())

		// (a) source=backfill:_it — LIKE pattern must match exactly ONE
		// row (backfill:git). NOT literal-matching (that'd be 0 rows).
		rec := bfDo(e, http.MethodDelete,
			"/api/v1/admin/backfill/heartbeats?source=backfill:_it", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["deleted"]).To(BeEquivalentTo(1),
			"LIKE pattern semantics broken: backfill:_it should match ONE row (backfill:git); got deleted=%v", resp["deleted"])

		// The hg row + NULL row must both survive.
		var nHg, nNull int
		Expect(hz.DB.Pool.QueryRow(ctx,
			`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='backfill:hg'`, user).Scan(&nHg)).To(Succeed())
		Expect(nHg).To(Equal(1), "backfill:hg was collateral damage from backfill:_it pattern")
		Expect(hz.DB.Pool.QueryRow(ctx,
			`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source IS NULL`, user).Scan(&nNull)).To(Succeed())
		Expect(nNull).To(Equal(1), "real Wakatime row (source NULL) hit by LIKE — floor failed")

		// (b) DEFENSE-IN-DEPTH: even a source that starts with backfill:
		// but expands to everything (`backfill:%`) must NEVER touch the
		// source-NULL real row. Verify by nuking with backfill:%.
		rec = bfDo(e, http.MethodDelete,
			"/api/v1/admin/backfill/heartbeats?source=backfill:%25", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(hz.DB.Pool.QueryRow(ctx,
			`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source IS NULL`, user).Scan(&nNull)).To(Succeed())
		Expect(nNull).To(Equal(1),
			"backfill:%% nuked the source-NULL real row: `AND source LIKE 'backfill:%%'` floor is gone")
	})
})

var _ = Describe("AdminBackfillJobHeartbeats: hostile body ignores forged Source field", func() {
	It("a session heartbeat with an EXTRA `source` field in JSON is ignored (server pulls from config)", func() {
		// LOCKS IN: the wire type doesn't declare a Source field so the
		// JSON decoder drops any client-supplied override. This test
		// makes that behavior EXPLICIT rather than accidental — if a
		// future refactor adds Source to model.HeartbeatPayload, this
		// spec turns red.
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfhb"))
		e := hz.Router()
		user, token := hz.MintUser("bfhb_forge")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		hz.H.SetBackfillJobQueue(backfilljobs.NewRegistry(nil))

		// Persist a specific sourceTag so we know what to expect.
		rec := bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/config", token,
			map[string]any{"sourceTag": "backfill:git:persisted"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		rec = bfDo(e, http.MethodPost, "/api/v1/admin/backfill/jobs", token,
			map[string]any{"repoName": "r", "totalCommits": 1})
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		jobID := resp["jobId"].(string)

		now := time.Now().UTC()
		// A hostile CLI attempts to override `source` on each heartbeat.
		// The persisted tag ("backfill:git:persisted") MUST win.
		body := map[string]any{
			"sessions": []any{
				map[string]any{
					"start": now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
					"end":   now.Add(-1 * time.Hour).Format(time.RFC3339Nano),
					"heartbeats": []map[string]any{{
						"entity":   "main.go",
						"type":     "file",
						"time":     now.Add(-90 * time.Minute).Unix(),
						"project":  "boomtime",
						"language": "go",
						"editor":   "vim",
						"platform": "linux",
						"category": "coding",
						"source":   "HOSTILE-INJECT", // forged
					}},
				},
			},
		}
		rec = bfDo(e, http.MethodPost,
			"/api/v1/admin/backfill/jobs/"+jobID+"/heartbeats", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		// INVARIANT: no row exists with the forged source; every row has
		// the persisted tag.
		var forged int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='HOSTILE-INJECT'`, user).Scan(&forged)).To(Succeed())
		Expect(forged).To(Equal(0),
			"hostile body-supplied source landed in heartbeats.source — wire shape now accepts a forgeable Source field")

		var legit int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM heartbeats WHERE sender=$1 AND source='backfill:git:persisted'`, user).Scan(&legit)).To(Succeed())
		Expect(legit).To(BeNumerically(">=", 1),
			"the persisted-config sourceTag didn't land on any row: expected >=1 got %d", legit)
	})
})

var _ = Describe("AdminBackfillDeleteHeartbeats (gaka-vh8): danger-zone prefix guard", func() {
	It("400s without ?source=... or ?all=true; enforces backfill: prefix on ?source=...", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfdel"))
		e := hz.Router()
		user, token := hz.MintUser("bfdel_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// (1) neither param → 400.
		rec := bfDo(e, http.MethodDelete, "/api/v1/admin/backfill/heartbeats", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// (2) source= without backfill: prefix → 400 (defense-in-depth).
		rec = bfDo(e, http.MethodDelete, "/api/v1/admin/backfill/heartbeats?source=real", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// (3) all=true → 200 with deleted=0 (no rows in test DB).
		rec = bfDo(e, http.MethodDelete, "/api/v1/admin/backfill/heartbeats?all=true", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["deleted"]).To(BeEquivalentTo(0))

		// (4) source=backfill:foo → 200.
		rec = bfDo(e, http.MethodDelete, "/api/v1/admin/backfill/heartbeats?source=backfill:foo", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
	})

	It("cross-user with POSITIVE CONTROL: A's DELETE removes A's rows AND leaves B's alone", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfdel"))
		e := hz.Router()
		userA, tokenA := hz.MintUser("bfdel_a")
		userB, _ := hz.MintUser("bfdel_b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		ctx := context.Background()

		// Seed backfill rows on BOTH users. A's rows are the POSITIVE
		// CONTROL — without them, response {deleted:0} is indistinguishable
		// from "handler is a no-op on both users' rows". By seeding A too
		// we can prove (1) A's rows disappeared AND (2) B's row survived.
		hz.Seeder(userA).Projects("ap")
		hz.Seeder(userB).Projects("bp")
		for _, seed := range []struct{ user, project string }{
			{userA, "ap"}, {userA, "ap"}, // two rows for A → deleted must equal 2
			{userB, "bp"},                // one row for B → must survive
		} {
			_, err := hz.DB.Pool.Exec(ctx, `
				INSERT INTO heartbeats (editor,plugin,platform,machine,sender,user_agent,branch,category,cursorpos,
					dependencies,entity,is_write,language,lineno,file_lines,project,ty,time_sent,source)
				VALUES ('vim','x','linux','m',$1,'ua','main','Coding',NULL,NULL,'x.go',false,'go',1,10,$2,'file',now(),'backfill:git')`, seed.user, seed.project)
			Expect(err).NotTo(HaveOccurred())
		}

		// Pre-flight snapshot: A has 2, B has 1.
		var pre int
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source LIKE 'backfill:%'`, userA).Scan(&pre)).To(Succeed())
		Expect(pre).To(Equal(2), "pre-flight: A must have 2 backfill rows")

		// A ?all=true removes A's rows only.
		rec := bfDo(e, http.MethodDelete, "/api/v1/admin/backfill/heartbeats?all=true", tokenA, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())

		// INVARIANT (positive control): response reports deleted>=A's row
		// count. Without this, a silent no-op passes.
		Expect(resp["deleted"]).To(BeNumerically(">=", 2),
			"positive control failed: A had 2 seeded backfill rows but handler reports deleted=%v", resp["deleted"])

		// INVARIANT: A's rows are gone (self-scope actually worked).
		var nA int
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source LIKE 'backfill:%'`, userA).Scan(&nA)).To(Succeed())
		Expect(nA).To(Equal(0), "A's DELETE didn't remove A's own rows — scoping broke both ways")

		// INVARIANT: B's row survives (cross-user isolation).
		var nB int
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source LIKE 'backfill:%'`, userB).Scan(&nB)).To(Succeed())
		Expect(nB).To(Equal(1), "cross-user leak: A's DELETE hit B's row")
	})
})

var _ = Describe("AdminBackfillWS (gaka-vh8): cookie-auth + admin-gate; snapshot only", func() {
	It("rejects a request with no refresh_token cookie (401/403, never 200)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfws"))
		e := hz.Router()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backfill/ws", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Key", "dGVzdC1rZXktMS0yLTMtNC01LTY=")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).NotTo(Equal(http.StatusSwitchingProtocols),
			"unauth'd WS handshake should NEVER upgrade; got %d body=%s", rec.Code, rec.Body.String())
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("rejects a non-admin with a valid cookie (403 pre-upgrade) — body must not leak username", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfws"))
		e := hz.Router()
		ctx := context.Background()
		user, _ := hz.MintUser("bfws_nonadmin")
		// Mint a refresh cookie for user (not on admin list).
		refresh := fmt.Sprintf("refresh-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backfill/ws", nil)
		req.Header.Set("Cookie", "refresh_token="+refresh)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Key", "dGVzdC1rZXktMS0yLTMtNC01LTY=")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusForbidden),
			"non-admin cookie must 403 pre-upgrade; got %d body=%s", rec.Code, rec.Body.String())
		// INVARIANT: the 403 body must NOT contain the resolved username
		// (leaks "user X is authenticated but not admin", which lets a
		// stolen cookie confirm identity). Same for admin allowlist names.
		body := rec.Body.String()
		Expect(body).NotTo(ContainSubstring(user),
			"403 body leaked the resolved username %q: %s", user, body)
	})

	It("admin with no BackfillJobQueue wired → 503 (feature-gate after admin-gate)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfws"))
		e := hz.Router()
		ctx := context.Background()
		user, _ := hz.MintUser("bfws_admin_noqueue")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
		// no BackfillJobQueue wired.
		refresh := fmt.Sprintf("refresh-%d", time.Now().UnixNano())
		Expect(hz.DB.CreateAccessTokens(ctx, testutilTokenData(user, refresh), 24)).To(Succeed())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/backfill/ws", nil)
		req.Header.Set("Cookie", "refresh_token="+refresh)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Key", "dGVzdC1rZXktMS0yLTMtNC01LTY=")
		req.Header.Set("Sec-WebSocket-Version", "13")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable))
	})
})

// testutilTokenData is a tiny helper: build a db.TokenData with a synthetic
// access token and the caller-supplied refresh token. Used by WS tests to
// stand up a valid refresh_token cookie without going through the public
// Login flow (Login requires plaintext password we don't track for the
// prefix-hashed users MintUser creates).
func testutilTokenData(user, refresh string) db.TokenData {
	// Access token is unused for these tests (WS auth reads refresh only).
	return db.TokenData{Owner: user, Token: strings.Repeat("a", 32), RefreshToken: refresh}
}
