// handler_test.go — HTTP integration tests for POST /api/v1/query (gaka-174.q).
//
// Non-tautological invariants each case pins:
//   - a valid coding group query returns real Groups computed from seeded rows
//     (not an empty/echoed shape) — the DSL actually reached Postgres and
//     aggregated;
//   - a scalar coding query sums the seeded seconds — the scalar arm works;
//   - an unknown measure / unknown domain is rejected 400 by Compile's
//     whitelist BEFORE any SQL runs (no panic, no 500);
//   - the reading domain is 404 when BooksEnabled is off (the default harness
//     config) — gated exactly like the other books routes, no oracle;
//   - a bad credential is 401 (fail-closed) before the body is even parsed;
//   - owner scoping holds: alice's query never sees bob's seeded rows.
package queryapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// doQuery posts a spec to /api/v1/query with the given bearer token.
func doQuery(e http.Handler, token string, spec any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if spec != nil {
		Expect(json.NewEncoder(&buf).Encode(spec)).To(Succeed())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query", &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// queryResponse mirrors the handler's JSON envelope for assertion.
type queryResponse struct {
	Kind   string   `json:"kind"`
	Scalar *float64 `json:"scalar"`
	Series []struct {
		Bucket string  `json:"bucket"`
		Value  float64 `json:"value"`
	} `json:"series"`
	Groups []struct {
		Key   string             `json:"key"`
		Value float64            `json:"value"`
		Count *int               `json:"count"`
		Stats map[string]float64 `json:"stats"`
	} `json:"groups"`
	Rows  []map[string]any `json:"rows"`
	Total *int             `json:"total"`
}

// seedReadingItem inserts one reading_items row for the queryapi rollup/rows tests.
func seedReadingItem(hz *testutil.Harness, owner, asin, title, series string, runtimeMin int, finished bool) {
	var fin any
	if finished {
		fin = time.Now().UTC().AddDate(0, 0, -1)
	}
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_items (owner, source, external_id, title, status, series,
			runtime_min, finished, finished_at)
		VALUES ($1,'audible',$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (owner, source, external_id) DO NOTHING`,
		owner, asin, title, statusFor(finished), series, runtimeMin, finished, fin)
	Expect(err).NotTo(HaveOccurred())
}

func statusFor(finished bool) string {
	if finished {
		return "read"
	}
	return "reading"
}

var _ = Describe("POST /api/v1/query (gaka-174.q)", func() {
	It("runs a valid coding group query and returns real aggregated groups", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("q_coding_groups")

		yesterday := time.Now().UTC().AddDate(0, 0, -1)
		Expect(hz.SeedRollup(owner, yesterday, "Go", 3600)).To(Succeed())

		rec := doQuery(e, token, map[string]any{
			"domain":  "coding",
			"measure": "seconds",
			"group":   "project",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("groups"))
		Expect(resp.Groups).To(HaveLen(1), "body=%s", rec.Body.String())
		// SeedRollup writes project 'P'; the group key is the raw column value.
		Expect(resp.Groups[0].Key).To(Equal("P"))
		Expect(resp.Groups[0].Value).To(Equal(float64(3600)))
	})

	It("runs a scalar coding query summing the seeded seconds", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("q_coding_scalar")

		day := time.Now().UTC().AddDate(0, 0, -1)
		Expect(hz.SeedRollup(owner, day, "Go", 1200)).To(Succeed())
		Expect(hz.SeedRollup(owner, day.AddDate(0, 0, -1), "Rust", 800)).To(Succeed())

		rec := doQuery(e, token, map[string]any{
			"domain":  "coding",
			"measure": "seconds",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("scalar"))
		Expect(resp.Scalar).NotTo(BeNil())
		Expect(*resp.Scalar).To(Equal(float64(2000)))
	})

	It("filters by a leaf predicate (only matching rows counted)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("q_coding_where")

		day := time.Now().UTC().AddDate(0, 0, -1)
		Expect(hz.SeedRollup(owner, day, "Go", 1000)).To(Succeed())
		Expect(hz.SeedRollup(owner, day.AddDate(0, 0, -1), "Rust", 500)).To(Succeed())

		rec := doQuery(e, token, map[string]any{
			"domain":  "coding",
			"measure": "seconds",
			"where":   map[string]any{"kind": "leaf", "dim": "language", "op": "eq", "values": []string{"Go"}},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("scalar"))
		Expect(resp.Scalar).NotTo(BeNil())
		Expect(*resp.Scalar).To(Equal(float64(1000)), "predicate should exclude the Rust row")
	})

	It("rejects an unknown measure with 400 (no panic, no 500)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("q_bad_measure")

		rec := doQuery(e, token, map[string]any{
			"domain":  "coding",
			"measure": "bananas",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("rejects an unknown domain with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("q_bad_domain")

		rec := doQuery(e, token, map[string]any{
			"domain":  "chickens",
			"measure": "seconds",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
	})

	It("gates the reading domain behind BooksEnabled (404 when off)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("q_reading_gated")

		// The default harness config leaves FeatureBooks off → BooksEnabled()==false.
		Expect(hz.Cfg.BooksEnabled()).To(BeFalse(), "precondition: books feature should default off")

		rec := doQuery(e, token, map[string]any{
			"domain":  "reading",
			"measure": "books",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound), "body=%s", rec.Body.String())
	})

	It("serves the reading domain when BooksEnabled is on", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureBooks = true // flip the gate on for this harness
		e := hz.Router()
		_, token := hz.MintUser("q_reading_on")

		rec := doQuery(e, token, map[string]any{
			"domain":  "reading",
			"measure": "books",
		})
		// No reading rows seeded → count(*) == 0, but the domain is reachable
		// (not 404) and the scalar arm resolves.
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("scalar"))
		Expect(resp.Scalar).NotTo(BeNil())
		Expect(*resp.Scalar).To(Equal(float64(0)))
	})

	It("returns per-group rollups (count + stats) for a grouped reading query", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureBooks = true
		e := hz.Router()
		owner, token := hz.MintUser("q_rollups_http")

		// series "Foundation": 2 rows, runtime 1320, finished 1.
		seedReadingItem(hz, owner, "F1", "f1", "Foundation", 600, true)
		seedReadingItem(hz, owner, "F2", "f2", "Foundation", 720, false)
		// series "Dune": 1 row, runtime 300, finished 1.
		seedReadingItem(hz, owner, "D1", "d1", "Dune", 300, true)

		rec := doQuery(e, token, map[string]any{
			"domain":  "reading",
			"measure": "books",
			"group":   "series",
			"rollups": []string{"runtime", "finished"},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("groups"))
		by := map[string]struct {
			Count *int
			Stats map[string]float64
			Value float64
		}{}
		for _, g := range resp.Groups {
			by[g.Key] = struct {
				Count *int
				Stats map[string]float64
				Value float64
			}{g.Count, g.Stats, g.Value}
		}
		f := by["Foundation"]
		Expect(f.Count).NotTo(BeNil())
		Expect(*f.Count).To(Equal(2))
		Expect(f.Stats["count"]).To(Equal(float64(2)))
		Expect(f.Stats["runtime"]).To(Equal(float64(1320)))
		Expect(f.Stats["finished"]).To(Equal(float64(1)))
		Expect(f.Value).To(Equal(float64(2)), "primary measure (books) stays in value")
		d := by["Dune"]
		Expect(d.Stats["runtime"]).To(Equal(float64(300)))
		Expect(d.Stats["finished"]).To(Equal(float64(1)))
	})

	It("returns owner-scoped paginated leaf rows with a total", func() {
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.FeatureBooks = true
		e := hz.Router()
		owner, token := hz.MintUser("q_rows_http")
		otherOwner, _ := hz.MintUser("q_rows_http_other")

		seedReadingItem(hz, owner, "R1", "Alpha", "S", 10, true)
		seedReadingItem(hz, owner, "R2", "Bravo", "S", 20, true)
		seedReadingItem(hz, owner, "R3", "Chuck", "S", 30, true)
		seedReadingItem(hz, otherOwner, "X1", "OtherBook", "S", 99, true)

		rec := doQuery(e, token, map[string]any{
			"domain": "reading",
			"rows":   true,
			"page":   map[string]any{"number": 1, "size": 2},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Kind).To(Equal("rows"))
		Expect(resp.Total).NotTo(BeNil())
		Expect(*resp.Total).To(Equal(3), "total counts all owner rows, not just the page")
		Expect(resp.Rows).To(HaveLen(2), "page size caps the returned rows")
		// FE JSON keys are preserved (directly castable to ReadingItemDTO).
		Expect(resp.Rows[0]).To(HaveKey("externalId"))
		Expect(resp.Rows[0]).To(HaveKey("title"))
		for _, r := range resp.Rows {
			Expect(r["title"]).NotTo(Equal("OtherBook"), "owner scope breached")
		}
	})

	It("fails closed (401) on a bad credential", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		rec := doQuery(e, "deadbeefdeadbeefdeadbeef", map[string]any{
			"domain":  "coding",
			"measure": "seconds",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusUnauthorized), "body=%s", rec.Body.String())
	})

	It("is owner-scoped: alice's query never sees bob's rows", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceOwner, aliceTok := hz.MintUser("q_scope_alice")
		bobOwner, _ := hz.MintUser("q_scope_bob")

		day := time.Now().UTC().AddDate(0, 0, -1)
		Expect(hz.SeedRollup(aliceOwner, day, "Go", 100)).To(Succeed())
		Expect(hz.SeedRollup(bobOwner, day, "Go", 9999)).To(Succeed())

		rec := doQuery(e, aliceTok, map[string]any{
			"domain":  "coding",
			"measure": "seconds",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var resp queryResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Scalar).NotTo(BeNil())
		Expect(*resp.Scalar).To(Equal(float64(100)), "alice's scalar leaked bob's 9999")
	})
})
