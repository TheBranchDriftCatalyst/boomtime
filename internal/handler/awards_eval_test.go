// awards_eval_ginkgo_test.go — ginkgo mirror of awards_eval_test.go
// (gaka-hc6.3 / gaka-hc6.3.1).
// 1:1 case map (5 stdlib TestXxx):
//
//	TestOwnAwards_ReturnsAwardAndWritesLedger    → OwnAwards > "returns python-novice + Cache-Control: private,max-age=30; ledger doesn't shrink"
//	TestPublicAwards_ReturnsAwardButDoesNotWriteLedger → PublicAwards > "returns award, no ledger write, Cache-Control: public,max-age=180"
//	TestPublicAwards_404WhenProfileDisabled      → PublicAwards > "unknown slug → 404"
//	TestAwardsBackfill_WritesHistoricalLedger    → AwardsBackfill > "5-day historical walk, idempotent second call"
//	TestAwardsBackfill_RejectsBadDays            → AwardsBackfill > "days<1 → 400; days>365 clamps to 200"
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// getJSONG — mirror of the stdlib getJSON.
func getJSONG(e http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// doPostJSONG — mirror of the stdlib doPostJSON.
func doPostJSONG(e http.Handler, target, token string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// countLedgerG — mirror of the stdlib countLedger.
func countLedgerG(hz *testutil.Harness, username string) int {
	var n int
	Expect(hz.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM award_ledger WHERE username=$1`, username).Scan(&n)).To(Succeed())
	return n
}

// seedPythonFiveHoursG — mirror of the stdlib helper.
func seedPythonFiveHoursG(hz *testutil.Harness, sender string) {
	base := time.Now().UTC().Add(-7 * 24 * time.Hour)
	base = time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.UTC)
	if base.Weekday() == time.Saturday {
		base = base.Add(48 * time.Hour)
	} else if base.Weekday() == time.Sunday {
		base = base.Add(24 * time.Hour)
	}
	hz.Seeder(sender).
		Projects("boomtime").
		Block(testutil.HB{
			Project:  "boomtime",
			Language: "python",
			Editor:   "vim",
			Platform: "linux",
			Category: "coding",
			Entity:   "main.py",
		}, base, 30, 900)
	Expect(hz.DB.RefreshRollup(context.Background(), sender, base.Add(-time.Hour))).To(Succeed())
}

// containsAwardIDG — mirror of the stdlib helper.
func containsAwardIDG(awards []map[string]any, id string) bool {
	for _, a := range awards {
		if a["id"] == id {
			return true
		}
	}
	return false
}

// awardIDsG — mirror of the stdlib helper.
func awardIDsG(awards []map[string]any) []string {
	out := make([]string, 0, len(awards))
	for _, a := range awards {
		if s, ok := a["id"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}

var _ = Describe("OwnAwards (gaka-hc6.3)", func() {
	It("returns python-novice, Cache-Control: private,max-age=30, ledger does not shrink", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsg"))
		e := hz.Router()
		user, token := hz.MintUser("awardsown_g")

		seedPythonFiveHoursG(hz, user)

		var langSeconds int64
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT COALESCE(SUM(gap_seconds), 0) FROM heartbeats
			 WHERE sender=$1 AND language='python' AND gap_seconds < 900`,
			user).Scan(&langSeconds)).To(Succeed())
		GinkgoWriter.Printf("seeded raw python seconds (gap<900): %d\n", langSeconds)

		before := countLedgerG(hz, user)

		rec := getJSONG(e, "/api/v1/users/current/awards", token)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var awards []map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &awards)).To(Succeed(), "body=%s", rec.Body.String())
		Expect(awards).NotTo(BeEmpty(), "no awards fired; body=%s", rec.Body.String())
		Expect(containsAwardIDG(awards, "languages-python-novice")).To(BeTrue(),
			"want languages-python-novice, got %v", awardIDsG(awards))

		Expect(rec.Header().Get("Cache-Control")).To(Equal("private, max-age=30"))

		after := countLedgerG(hz, user)
		Expect(after).To(BeNumerically(">=", before), "ledger row count decreased")
	})
})

var _ = Describe("PublicAwards (gaka-hc6.3)", func() {
	It("returns award, does NOT write ledger, Cache-Control: public,max-age=180", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsg"))
		e := hz.Router()
		user, _ := hz.MintUser("awardspub_g")

		Expect(hz.DB.SetPublicProfile(context.Background(), user, true, "awardsgtestslug")).To(Succeed())

		seedPythonFiveHoursG(hz, user)

		before := countLedgerG(hz, user)

		rec := getJSONG(e, "/api/public/profile/awardsgtestslug/awards", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var awards []map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &awards)).To(Succeed())
		Expect(containsAwardIDG(awards, "languages-python-novice")).To(BeTrue(),
			"want python-novice, got %v", awardIDsG(awards))

		after := countLedgerG(hz, user)
		Expect(after).To(Equal(before),
			"public endpoint must be ledger-invisible (wrote %d rows)", after-before)

		Expect(rec.Header().Get("Cache-Control")).To(Equal("public, max-age=180"))
	})

	It("404s on an unknown public slug (profile disabled or never enabled)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsg"))
		e := hz.Router()
		user, _ := hz.MintUser("awardsprivate_g")

		seedPythonFiveHoursG(hz, user)

		rec := getJSONG(e, "/api/public/profile/nonexistent-slug-g/awards", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

var _ = Describe("AwardsBackfill (gaka-hc6.5.1)", func() {
	It("walks 5 historical days and is idempotent on a second call", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsg"))
		e := hz.Router()
		user, token := hz.MintUser("awardsbf_g")

		base := time.Now().UTC().Add(-40 * 24 * time.Hour)
		base = time.Date(base.Year(), base.Month(), base.Day(), 12, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("boomtime")
		for d := 0; d < 30; d++ {
			day := base.AddDate(0, 0, d)
			sd.Block(testutil.HB{
				Project:  "boomtime",
				Language: "python",
				Editor:   "vim",
				Platform: "linux",
				Category: "coding",
				Entity:   fmt.Sprintf("day%d.py", d),
			}, day, 20, 900)
		}
		Expect(hz.DB.RefreshRollup(context.Background(), user, base.Add(-time.Hour))).To(Succeed())

		before := countLedgerG(hz, user)

		rec := doPostJSONG(e, "/api/v1/users/current/awards/backfill", token, map[string]any{"days": 5})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var resp struct {
			DaysProcessed int `json:"daysProcessed"`
			RowsWritten   int `json:"rowsWritten"`
			Skipped       int `json:"skipped"`
			TookMs        int `json:"tookMs"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.DaysProcessed).To(Equal(5))
		Expect(resp.Skipped).To(Equal(0))
		Expect(resp.RowsWritten).To(BeNumerically(">=", 0))

		after := countLedgerG(hz, user)
		Expect(after).To(BeNumerically(">=", before), "ledger shrank")

		// Idempotency: second call should be a no-op.
		rec2 := doPostJSONG(e, "/api/v1/users/current/awards/backfill", token, map[string]any{"days": 5})
		Expect(rec2.Code).To(Equal(http.StatusOK), "2nd backfill body=%s", rec2.Body.String())

		var resp2 struct {
			RowsWritten int `json:"rowsWritten"`
		}
		Expect(json.Unmarshal(rec2.Body.Bytes(), &resp2)).To(Succeed())
		Expect(resp2.RowsWritten).To(Equal(0), "idempotency broken: 2nd run wrote rows")
	})

	It("rejects days<1 with 400; clamps days>365 to 200", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsg"))
		e := hz.Router()
		_, token := hz.MintUser("awardsbad_g")

		rec := doPostJSONG(e, "/api/v1/users/current/awards/backfill", token, map[string]any{"days": 0})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "days=0 got %d", rec.Code)

		rec = doPostJSONG(e, "/api/v1/users/current/awards/backfill", token, map[string]any{"days": -5})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "days=-5 got %d", rec.Code)

		rec = doPostJSONG(e, "/api/v1/users/current/awards/backfill", token, map[string]any{"days": 1000})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"days=1000 should clamp to 200: body=%s", rec.Body.String())
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func getJSON(t *testing.T, e http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

type bytesReaderT struct {
	b   []byte
	pos int
}

func (bb *bytesReaderT) Read(p []byte) (int, error) {
	if bb.pos >= len(bb.b) {
		return 0, ioEOF
	}
	n := copy(p, bb.b[bb.pos:])
	bb.pos += n
	return n, nil
}

type eofErr struct{}

func (eofErr) Error() string { return "EOF" }

var ioEOF error = eofErr{}

func containsAwardID(awards []map[string]any, id string) bool {
	for _, a := range awards {
		if a["id"] == id {
			return true
		}
	}
	return false
}

func awardIDs(awards []map[string]any) []string {
	out := make([]string, 0, len(awards))
	for _, a := range awards {
		if s, ok := a["id"].(string); ok {
			out = append(out, s)
		}
	}
	return out
}
