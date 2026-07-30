// derived_test.go — gaka-d6x.handler: cover DerivedStatus/DerivedResync.
// Named invariants:
//
//	"unauth → 4xx no leak" — a missing/bad token returns 4xx BEFORE
//	touching the DB (fail-closed).
//
//	"per-owner counts on /derived/status" — alice's DerivedStatus reports
//	numeric counts derived from HER heartbeats only. Bob's status on the
//	same shared DB returns zeros. DerivedStatus has no username/sender
//	field in its payload (see internal/db/ingest.go: {heartbeats,
//	gapPopulated, gapMissing, rollupRows, ...}), so a naive substring
//	check on the other username is vacuously true and proves nothing —
//	the numeric zero-vs-nonzero split is the actual owner-scoping proof.
//
//	"resync invalidates the owner's cached /stats aggregates" — the
//	real chain of events: seed → read /stats (populates cache) → mutate
//	underlying data directly via DB helper → read /stats again (still
//	cached, same bytes as read 1) → POST /derived/resync → read /stats
//	a third time (fresh, different bytes). If invalidateOwnerCache is
//	silently a no-op, the third read still matches the first two and
//	this test fails.
package stats_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Derived endpoints (gaka-d6x.handler)", func() {
	It("rejects unauthenticated GET /derived/status with 4xx (fail-closed)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/current/derived/status", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("rejects unauthenticated POST /derived/resync with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/derived/resync", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("GET /derived/status returns per-owner counts (bob sees zeros, alice sees her real seeded totals)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("derivedA")
		_, bobTok := hz.MintUser("derivedB")

		// Seed alice with a real heartbeats block; bob stays empty.
		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(aliceUser).Projects("alpha")
		sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 4, 60)
		sd.RecomputeGaps(base.AddDate(0, 0, -1))
		sd.RefreshRollup(base.AddDate(0, 0, -1))

		// Alice: real counts.
		aRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/derived/status", aliceTok, nil)
		Expect(aRec).To(testutil.HaveStatus(http.StatusOK), "alice: %s", aRec.Body.String())
		var aPayload struct {
			Heartbeats    int64 `json:"heartbeats"`
			RollupRows    int64 `json:"rollupRows"`
			RollupSeconds int64 `json:"rollupSeconds"`
		}
		Expect(json.Unmarshal(aRec.Body.Bytes(), &aPayload)).To(Succeed(),
			"alice body: %s", aRec.Body.String())
		Expect(aPayload.Heartbeats).To(BeNumerically(">", 0),
			"alice has 4 seeded beats + 1 break beat; heartbeats count should be nonzero; got %+v",
			aPayload)
		Expect(aPayload.RollupRows).To(BeNumerically(">", 0),
			"RefreshRollup produced no rows for alice; got %+v", aPayload)

		// Bob: identical shared DB, no seeding for bob → sender-scoped counts must be zero.
		bRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/derived/status", bobTok, nil)
		Expect(bRec).To(testutil.HaveStatus(http.StatusOK), "bob: %s", bRec.Body.String())
		var bPayload struct {
			Heartbeats    int64 `json:"heartbeats"`
			RollupRows    int64 `json:"rollupRows"`
			RollupSeconds int64 `json:"rollupSeconds"`
		}
		Expect(json.Unmarshal(bRec.Body.Bytes(), &bPayload)).To(Succeed(),
			"bob body: %s", bRec.Body.String())
		Expect(bPayload.Heartbeats).To(BeZero(),
			"bob has no seeded beats — leakage if nonzero. got %+v", bPayload)
		Expect(bPayload.RollupRows).To(BeZero(),
			"bob has no rollup rows — leakage if nonzero. got %+v", bPayload)
		Expect(bPayload.RollupSeconds).To(BeZero(),
			"bob has no rollup seconds — leakage if nonzero. got %+v", bPayload)

		// The payload has no username field by construction; we do NOT assert
		// on username substrings because that would be a vacuously-true check
		// (the DerivedStatus struct in internal/db/ingest.go carries counts
		// and byte sizes but no sender label). Numeric zero-vs-nonzero is the
		// real owner-scoping proof.
	})

	It("POST /derived/resync invalidates the owner's cached /stats aggregates (3-read cache-bust proof)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("derivedRS")
		ctx := context.Background()

		// Seed heartbeats and derived, then hit /stats once to populate cache.
		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(user).Projects("alpha")
		sd.Block(testutil.HB{Project: "alpha", Language: "Go", Editor: "vim"}, base, 3, 120)
		sd.RecomputeGaps(base.AddDate(0, 0, -1))
		sd.RefreshRollup(base.AddDate(0, 0, -1))

		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		// Read 1: populates the owner-scoped cache.
		read1 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, token, nil)
		Expect(read1).To(testutil.HaveStatus(http.StatusOK))
		body1 := read1.Body.String()

		// Mutate the underlying data DIRECTLY through the DB helper (bypasses
		// the handler, so cache is NOT invalidated by this write). This is the
		// harness for the cache-invalidation assertion below — without a
		// mutation between reads 2 and 3 we can't prove the third read isn't
		// just serving a re-derived-identical payload.
		sd.Seed(testutil.HB{
			Project: "alpha", Language: "Go", Editor: "vim",
			TS: base.Add(4 * time.Minute), Gap: 300,
		})
		// Rebuild derived tables so /stats *would* return a different body if it
		// went to the DB — but the cache should still short-circuit us on read 2.
		Expect(hz.DB.RecomputeGaps(ctx, user, base.AddDate(0, 0, -1))).To(Succeed())
		Expect(hz.DB.RefreshRollup(ctx, user, base.AddDate(0, 0, -1))).To(Succeed())

		// Read 2: still cached (same TTL, no invalidate call yet). Bytes must
		// equal read 1 verbatim.
		read2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, token, nil)
		Expect(read2).To(testutil.HaveStatus(http.StatusOK))
		Expect(read2.Body.String()).To(Equal(body1),
			"cache should still be warm between read 1 and read 2 (no invalidate yet)")

		// Now: POST /derived/resync — its whole point (per derived.go) is to
		// call invalidateOwnerCache after rebuilding derived tables.
		resyncRec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/derived/resync", token, nil)
		Expect(resyncRec).To(testutil.HaveStatus(http.StatusOK), "resync: %s", resyncRec.Body.String())
		var resyncPayload struct {
			Heartbeats int64 `json:"heartbeats"`
			InSync     bool  `json:"inSync"`
		}
		Expect(json.Unmarshal(resyncRec.Body.Bytes(), &resyncPayload)).To(Succeed(),
			"resync body: %s", resyncRec.Body.String())
		Expect(resyncPayload.Heartbeats).To(BeNumerically(">", 0),
			"resync payload should report the sender's real derived state; got %+v", resyncPayload)

		// Read 3: after invalidateOwnerCache — must be re-computed from the
		// mutated DB, and thus must NOT equal the pre-mutation payload.
		read3 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/stats"+q, token, nil)
		Expect(read3).To(testutil.HaveStatus(http.StatusOK))
		Expect(read3.Body.String()).NotTo(Equal(body1),
			"cache-invalidation regression: /stats returned the pre-mutation cached body after /derived/resync (invalidateOwnerCache was a no-op)")
	})
})
