// awards_backfill_edges_test.go — extra edge coverage for AwardsBackfill
// (boom-hc6.5.1). The primary suite in awards_eval_test.go covers the
// happy path + days clamp; this file closes the remaining branches:
// (a) unauth'd caller → auth failure (never 200 — no oracle);
// (b) malformed JSON body → 400;
// (c) days=365 hard clamp (the >365 branch prescribed by req.Days = 365);
// (d) cross-user isolation on ledger writes.
package awards_test

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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("AwardsBackfill (boom-hc6.5.1) edge branches", func() {
	It("rejects an unauth'd caller with a pinned 4xx AND a body that leaks no internals", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awbfe"))
		e := hz.Router()

		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/users/current/awards/backfill",
			strings.NewReader(`{"days":5}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// PIN the exact auth-failure codes. NotTo(Equal(200)) would let a
		// 500 (crash) sneak through — we require a real 4xx that the auth
		// middleware chose deliberately.
		Expect(rec.Code).To(BeElementOf(
			http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest),
			"unauth'd backfill must return 401/403/400; got %d body=%s", rec.Code, rec.Body.String())
		// Body must not leak internals. This is the real invariant behind
		// "no oracle" — a 401 that echoes a DB path or the DSN password
		// would still be a leak even with the correct status code.
		body := rec.Body.String()
		for _, banned := range []string{
			"/Users/", // absolute path leak
			"panic",   // stack trace
			"pgx",     // DB driver name
			"postgres:",
			"sql:",
			".go:", // file:line from a stack
			"password",
			"BOOM_ENCRYPTION_KEY",
		} {
			Expect(body).NotTo(ContainSubstring(banned),
				"unauth response body leaked %q: %s", banned, body)
		}
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

	It("cross-user isolation with positive control: A's backfill writes ROWS FOR A and NEVER B", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awbfe"))
		e := hz.Router()
		userA, tokenA := hz.MintUser("awbfe_a")
		userB, _ := hz.MintUser("awbfe_b")

		// Seed BOTH users with award-earning python/day traffic. This is
		// the POSITIVE CONTROL — without it, A's backfill could be a
		// silent no-op and the "n==0 for B" assertion would still pass.
		base := time.Now().UTC().Add(-5 * 24 * time.Hour)
		base = time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.UTC)
		seedFor := func(u string) {
			sd := hz.Seeder(u).Projects("boomtime")
			for d := 0; d < 3; d++ {
				day := base.AddDate(0, 0, d)
				sd.Block(testutil.HB{
					Project: "boomtime", Language: "python", Editor: "vim",
					Platform: "linux", Category: "coding", Entity: u + ".py",
				}, day, 20, 900)
			}
			Expect(hz.DB.RefreshRollup(context.Background(), u, base.Add(-time.Hour))).To(Succeed())
		}
		seedFor(userA)
		seedFor(userB)

		// A runs backfill (A now owns heartbeats, so ledger writes MUST land).
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/users/current/awards/backfill",
			strings.NewReader(`{"days":3}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Basic "+tokenA)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		// INVARIANT 1: NO ledger rows landed for user B (A can't backfill B's history).
		var nB int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM award_ledger WHERE username=$1`, userB).Scan(&nB)).To(Succeed())
		Expect(nB).To(Equal(0),
			"cross-user leak: A's backfill wrote %d ledger rows for B", nB)

		// INVARIANT 2 (positive control): A's ledger DID grow — the
		// handler is not a silent no-op. Without this assertion the
		// scoping test can't distinguish "correctly scoped" from
		// "silently broken for everybody".
		var nA int
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM award_ledger WHERE username=$1`, userA).Scan(&nA)).To(Succeed())
		Expect(nA).To(BeNumerically(">", 0),
			"positive control failed: A had heartbeats but backfill wrote 0 ledger rows — handler is silently no-op'ing")
	})
})
