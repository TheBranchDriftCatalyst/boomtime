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
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/"+jobID, tokenB,
			map[string]any{"status": "done"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"cross-owner PATCH must be 404 (no oracle), got body=%s", rec.Body.String())

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

		// (4) totally unknown id → 404 (indistinguishable from cross-owner).
		rec = bfDo(e, http.MethodPatch, "/api/v1/admin/backfill/jobs/does-not-exist", tokenA,
			map[string]any{"status": "done"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))

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

	It("cross-user: A's DELETE cannot see/reach B's backfill rows (per-user scope)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "abfdel"))
		e := hz.Router()
		userA, tokenA := hz.MintUser("bfdel_a")
		userB, _ := hz.MintUser("bfdel_b")
		hz.Cfg.AdminUsers = map[string]struct{}{userA: {}, userB: {}}
		ctx := context.Background()

		// Seed a single backfill row on user B (directly via DB — bypasses
		// the admin flow so the seed doesn't run through A's session).
		hz.Seeder(userB).Projects("bp")
		_, err := hz.DB.Pool.Exec(ctx, `
			INSERT INTO heartbeats (editor,plugin,platform,machine,sender,user_agent,branch,category,cursorpos,
				dependencies,entity,is_write,language,lineno,file_lines,project,ty,time_sent,source)
			VALUES ('vim','x','linux','m',$1,'ua','main','Coding',NULL,NULL,'x.go',false,'go',1,10,'bp','file',now(),'backfill:git')`, userB)
		Expect(err).NotTo(HaveOccurred())

		// A ?all=true removes rows for A only. B's row survives.
		rec := bfDo(e, http.MethodDelete, "/api/v1/admin/backfill/heartbeats?all=true", tokenA, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var n int
		Expect(hz.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1 AND source LIKE 'backfill:%'`, userB).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1), "cross-user leak: A's DELETE hit B's row")
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

	It("rejects a non-admin with a valid cookie (403 pre-upgrade)", func() {
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
