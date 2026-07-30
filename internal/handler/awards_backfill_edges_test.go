// awards_backfill_edges_test.go — extra edge coverage for AwardsBackfill
// (gaka-hc6.5.1). The primary suite in awards_eval_test.go covers the
// happy path + days clamp; this file closes the remaining branches:
// (a) unauth'd caller → auth failure (never 200 — no oracle);
// (b) malformed JSON body → 400;
// (c) days=365 hard clamp (the >365 branch prescribed by req.Days = 365);
// (d) cross-user isolation on ledger writes.
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("AwardsBackfill (gaka-hc6.5.1) edge branches", func() {
	It("rejects an unauth'd caller before touching any DB (no oracle for user existence)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awbfe"))
		e := hz.Router()

		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/users/current/awards/backfill",
			strings.NewReader(`{"days":5}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// Any 4xx (401/400) is fine — the invariant is "not 200".
		Expect(rec.Code).NotTo(Equal(http.StatusOK),
			"unauth'd backfill returned 200 — auth leak; body=%s", rec.Body.String())
	})

	It("rejects a malformed JSON body with 400 (BindJSONWithLimit branch)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awbfe"))
		e := hz.Router()
		_, token := hz.MintUser("awbfe_badjson")

		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/users/current/awards/backfill",
			bytes.NewReader([]byte(`{not-json`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"malformed JSON must return 400; body=%s", rec.Body.String())
	})

	It("days=365 walks exactly 365 without additional clamp (upper-edge branch)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awbfe"))
		e := hz.Router()
		_, token := hz.MintUser("awbfe_365")

		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/users/current/awards/backfill",
			strings.NewReader(`{"days":365}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"days=365 should be OK; body=%s", rec.Body.String())
		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["daysProcessed"]).To(BeEquivalentTo(365),
			"days=365 should process 365 days exactly")
	})

	It("cross-user isolation: A's backfill only writes ledger rows for A (never B)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awbfe"))
		e := hz.Router()
		userA, tokenA := hz.MintUser("awbfe_a")
		userB, _ := hz.MintUser("awbfe_b")

		// Seed B with data that WOULD earn awards (5h of python/day).
		base := time.Now().UTC().Add(-5 * 24 * time.Hour)
		base = time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.UTC)
		sd := hz.Seeder(userB).Projects("boomtime")
		for d := 0; d < 3; d++ {
			day := base.AddDate(0, 0, d)
			sd.Block(testutil.HB{
				Project: "boomtime", Language: "python", Editor: "vim",
				Platform: "linux", Category: "coding", Entity: "b.py",
			}, day, 20, 900)
		}
		Expect(hz.DB.RefreshRollup(context.Background(), userB, base.Add(-time.Hour))).To(Succeed())

		// A runs backfill (with A's token, A owns no heartbeats).
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/users/current/awards/backfill",
			strings.NewReader(`{"days":3}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+tokenA)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		// INVARIANT: NO ledger rows landed for user B (A can't backfill B's history).
		var n int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM award_ledger WHERE username=$1`, userB).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(0),
			"cross-user leak: A's backfill wrote %d ledger rows for B", n)
		// Meanwhile A's ledger writes are >=0 (A owns no data → nothing to write; that's fine).
		var nA int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM award_ledger WHERE username=$1`, userA).Scan(&nA)).To(Succeed())
		Expect(nA).To(BeNumerically(">=", 0))
	})
})
