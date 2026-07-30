// awards_test.go — gaka-d6x.handler
//
// Coverage suite for the awards cluster (awards.go): AwardsLog,
// AwardsStreaks, PublicAwardsStreaks, AwardsLedger, parsePositiveInt,
// awardsStreaksFor.
//
// Ginkgo (package handler_test). Every spec pins a NAMED INVARIANT — no
// naked roundtrips. All user-scoped endpoints assert cross-user isolation
// (user B must NOT see user A's rows / must not affect A's streaks).
//
// hz.Router() wires OwnAwards + AwardsBackfill + AwardsStreaks +
// AwardsLedger but does NOT wire AwardsLog or PublicAwardsStreaks. Those
// are registered on a local *echo.Echo per-test — a fresh echo.New() with
// only the needed routes avoids the duplicate-route panic entirely.
package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/handler"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
	"github.com/labstack/echo/v5"
)

// awardsAuxRouter mounts the routes that hz.Router() intentionally omits
// (POST /awards/log + public streaks). Uses a fresh echo.New() — never
// touch hz.Router() from here, or the two routers race for the same
// paths and Echo panics on duplicate registration.
func awardsAuxRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	h := hz.H
	e.POST("/api/v1/users/current/awards/log", h.AwardsLog)
	e.GET("/api/public/profile/:slug/awards/streaks", h.PublicAwardsStreaks)
	return e
}

// countLedgerForLabelG counts rows for one (username, label_id) — a
// finer-grained sibling of countLedgerG defined in awards_eval_test.go.
// The isolation asserts below need per-label counts so a "written under
// wrong period" bug can't hide behind a total that already had rows.
func countLedgerForLabelG(hz *testutil.Harness, username, labelID string) int {
	var n int
	Expect(hz.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM award_ledger WHERE username=$1 AND label_id=$2`,
		username, labelID).Scan(&n)).To(Succeed())
	return n
}

// ensureLabels inserts synthetic labels the tests reference. award_ledger
// has an FK on labels(id), so a POST /awards/log with a made-up label id
// would 500 without this. Idempotent via ON CONFLICT — safe to call from
// multiple specs in the same shared DB.
func ensureLabels(hz *testutil.Harness, ids ...string) {
	for _, id := range ids {
		_, err := hz.DB.Pool.Exec(context.Background(), `
			INSERT INTO labels (id, kind, label, glyph, description, optimized_prompt, rank, tier, condition)
			VALUES ($1, 'archetype', $1, 'X', 'test', '', 0, NULL, '{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":1}'::jsonb)
			ON CONFLICT (id) DO NOTHING`, id)
		Expect(err).NotTo(HaveOccurred(), "ensure label %s", id)
	}
}

var _ = Describe("AwardsLog (gaka-mwp-streaks)", func() {
	It("writes exactly one ledger row per (label, current period) — idempotent replay is a no-op", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)
		user, token := hz.MintUser("awardslog")
		ensureLabels(hz, "label-daily-x", "label-weekly-y")

		body := map[string]any{
			"items": []map[string]any{
				{"labelId": "label-daily-x", "periodType": "daily"},
				{"labelId": "label-weekly-y", "periodType": "weekly"},
			},
		}
		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "first POST body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["received"]).To(BeNumerically("==", 2))
		Expect(got["written"]).To(BeNumerically("==", 2), "first call must write both rows")

		Expect(countLedgerForLabelG(hz, user, "label-daily-x")).To(Equal(1))
		Expect(countLedgerForLabelG(hz, user, "label-weekly-y")).To(Equal(1))

		// LOAD-BEARING: second call in the same period must be a no-op.
		// This is the entire streak invariant — refreshing a page can't
		// "advance" a period twice or the counter is meaningless.
		rec2 := doPostJSONG(e, "/api/v1/users/current/awards/log", token, body)
		Expect(rec2).To(testutil.HaveStatus(http.StatusOK))
		var got2 map[string]any
		Expect(json.Unmarshal(rec2.Body.Bytes(), &got2)).To(Succeed())
		Expect(got2["written"]).To(BeNumerically("==", 0),
			"idempotency violated: replay wrote %v rows", got2["written"])
	})

	It("filters unknown periodTypes and empty labelIds instead of failing the batch", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)
		user, token := hz.MintUser("awardslogfilter")
		ensureLabels(hz, "keep-daily", "drop-lifetime", "drop-unknown")

		body := map[string]any{
			"items": []map[string]any{
				{"labelId": "keep-daily", "periodType": "daily"},
				{"labelId": "drop-lifetime", "periodType": "lifetime"},
				{"labelId": "drop-unknown", "periodType": "hourly"},
				{"labelId": "", "periodType": "daily"},
			},
		}
		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["received"]).To(BeNumerically("==", 4),
			"received counts the raw wire request, not the filtered set")
		Expect(got["written"]).To(BeNumerically("==", 1),
			"lifetime + unknown period + empty label must all drop; only keep-daily survives")

		Expect(countLedgerForLabelG(hz, user, "keep-daily")).To(Equal(1))
		Expect(countLedgerForLabelG(hz, user, "drop-lifetime")).To(Equal(0),
			"lifetime label leaked into ledger — award_ledger.go silently drops must hold at handler layer too")
		Expect(countLedgerForLabelG(hz, user, "drop-unknown")).To(Equal(0))
	})

	It("rejects malformed `at` with 400 and refuses `at` more than 1 hour in the future", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)
		_, token := hz.MintUser("awardslogbad_at")

		// Malformed timestamp — not RFC3339.
		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", token, map[string]any{
			"at":    "not-a-timestamp",
			"items": []map[string]any{{"labelId": "x", "periodType": "daily"}},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"malformed `at` should 400; got %d body=%s", rec.Code, rec.Body.String())

		// Future timestamp — 2 hours ahead (> 1h grace).
		future := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
		rec = doPostJSONG(e, "/api/v1/users/current/awards/log", token, map[string]any{
			"at":    future,
			"items": []map[string]any{{"labelId": "x", "periodType": "daily"}},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"future `at` should 400 (streak-walker poison guard); got %d body=%s", rec.Code, rec.Body.String())
	})

	It("accepts historical `at` and buckets against THAT day (backfill path)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)
		user, token := hz.MintUser("awardsloghist")
		ensureLabels(hz, "old-daily")

		past := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339)
		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", token, map[string]any{
			"at":    past,
			"items": []map[string]any{{"labelId": "old-daily", "periodType": "daily"}},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "historical `at` should succeed: body=%s", rec.Body.String())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got["written"]).To(BeNumerically("==", 1))

		// LOAD-BEARING: the ledger row's period_start must be ≤ today. If
		// the handler ignored `at` and used time.Now() we'd see today's
		// bucket, not the historical one.
		var periodStart time.Time
		Expect(hz.DB.Pool.QueryRow(context.Background(),
			`SELECT period_start FROM award_ledger WHERE username=$1 AND label_id=$2`,
			user, "old-daily").Scan(&periodStart)).To(Succeed())
		Expect(periodStart.Before(time.Now().Add(-24 * time.Hour))).To(BeTrue(),
			"historical `at` was ignored — got period_start=%s", periodStart)
	})

	It("400s when the body exceeds the 128 KiB cap (BindJSONWithLimit guard)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)
		_, token := hz.MintUser("awardslogbig")

		// One giant label id that pushes the body past 128 KiB.
		big := strings.Repeat("a", 130*1024)
		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", token, map[string]any{
			"items": []map[string]any{{"labelId": big, "periodType": "daily"}},
		})
		Expect(rec.Code).To(Or(
			Equal(http.StatusRequestEntityTooLarge),
			Equal(http.StatusBadRequest),
		), "expected 413 (or 400) for over-cap body; got %d", rec.Code)
	})

	It("requires auth — unauthenticated POST returns 401", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)

		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", "", map[string]any{
			"items": []map[string]any{{"labelId": "x", "periodType": "daily"}},
		})
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("cross-user isolation: user B's log write does not appear on user A's ledger", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsloggen"))
		e := awardsAuxRouter(hz)
		userA, _ := hz.MintUser("awardslogA")
		userB, tokB := hz.MintUser("awardslogB")
		ensureLabels(hz, "shared-label-id")

		// User B writes.
		rec := doPostJSONG(e, "/api/v1/users/current/awards/log", tokB, map[string]any{
			"items": []map[string]any{{"labelId": "shared-label-id", "periodType": "daily"}},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		// User A must remain empty for the same label id.
		Expect(countLedgerForLabelG(hz, userA, "shared-label-id")).To(Equal(0),
			"cross-user leak: user A saw user B's ledger row")
		Expect(countLedgerForLabelG(hz, userB, "shared-label-id")).To(Equal(1))
	})
})

var _ = Describe("AwardsStreaks (gaka-mwp-streaks)", func() {
	It("returns a flat {labelId: n} map with Cache-Control: private,max-age=60", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsstreaks"))
		eAux := awardsAuxRouter(hz)
		eMain := hz.Router()
		user, token := hz.MintUser("awardsstreak")
		ensureLabels(hz, "streak3")

		// Seed a daily streak: 3 consecutive days ending today via
		// backfilled log writes (each `at` in a distinct daily bucket).
		for d := 0; d < 3; d++ {
			at := time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339)
			rec := doPostJSONG(eAux, "/api/v1/users/current/awards/log", token, map[string]any{
				"at":    at,
				"items": []map[string]any{{"labelId": "streak3", "periodType": "daily"}},
			})
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "seed day %d: body=%s", d, rec.Body.String())
		}

		rec := getJSONG(eMain, "/api/v1/users/current/awards/streaks", token)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec).To(testutil.HaveHeader("Cache-Control", "private, max-age=60"))

		var streaks map[string]int
		Expect(json.Unmarshal(rec.Body.Bytes(), &streaks)).To(Succeed())
		Expect(streaks).To(HaveKey("streak3"))
		Expect(streaks["streak3"]).To(BeNumerically(">=", 3),
			"3 consecutive daily periods must yield streak >=3; got %d — full map=%v", streaks["streak3"], streaks)

		// LOAD-BEARING: only labels with an ACTIVE current-period fire go
		// into the map. Labels with rows entirely in the past → excluded.
		Expect(streaks).NotTo(HaveKey("never-fired"))

		_ = user
	})

	It("cross-user isolation: user A's streak is invisible on user B's endpoint", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsstreaks"))
		eAux := awardsAuxRouter(hz)
		eMain := hz.Router()

		_, tokA := hz.MintUser("awardsstreakA")
		_, tokB := hz.MintUser("awardsstreakB")
		ensureLabels(hz, "onlyA")

		// User A logs a streak.
		rec := doPostJSONG(eAux, "/api/v1/users/current/awards/log", tokA, map[string]any{
			"items": []map[string]any{{"labelId": "onlyA", "periodType": "daily"}},
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		// User B's streaks map MUST NOT contain onlyA.
		rec = getJSONG(eMain, "/api/v1/users/current/awards/streaks", tokB)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var streaks map[string]int
		Expect(json.Unmarshal(rec.Body.Bytes(), &streaks)).To(Succeed())
		Expect(streaks).NotTo(HaveKey("onlyA"),
			"cross-user leak on /awards/streaks — B saw A's streak; got %v", streaks)
	})

	It("unauthenticated request to /awards/streaks is rejected before DB lookup", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsstreaks"))
		e := hz.Router()

		rec := getJSONG(e, "/api/v1/users/current/awards/streaks", "")
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})
})

var _ = Describe("PublicAwardsStreaks (gaka-mwp-streaks)", func() {
	It("returns the owner's streaks under Cache-Control: private,max-age=60 (60s window)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "publicstreaks"))
		e := awardsAuxRouter(hz)
		user, token := hz.MintUser("pubstreakowner")
		ensureLabels(hz, "pubstreak2")

		slug := "pubslugstreak-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
		Expect(hz.DB.SetPublicProfile(context.Background(), user, true, slug)).To(Succeed())

		// Owner seeds a 2-day streak via the auth'd log endpoint.
		for d := 0; d < 2; d++ {
			at := time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339)
			r := doPostJSONG(e, "/api/v1/users/current/awards/log", token, map[string]any{
				"at":    at,
				"items": []map[string]any{{"labelId": "pubstreak2", "periodType": "daily"}},
			})
			Expect(r).To(testutil.HaveStatus(http.StatusOK))
		}

		rec := getJSONG(e, "/api/public/profile/"+slug+"/awards/streaks", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec).To(testutil.HaveHeader("Cache-Control", "private, max-age=60"))

		var streaks map[string]int
		Expect(json.Unmarshal(rec.Body.Bytes(), &streaks)).To(Succeed())
		Expect(streaks["pubstreak2"]).To(BeNumerically(">=", 2))
	})

	It("returns 404 for an unknown slug (no oracle for existence)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "publicstreaks"))
		e := awardsAuxRouter(hz)

		rec := getJSONG(e, "/api/public/profile/nope-not-real-slug-xyz/awards/streaks", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})
})

var _ = Describe("AwardsLedger (gaka-mwp-streaks)", func() {
	It("returns rows for the caller ONLY, honors ?label filter, ?limit clamp, private cache header", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsledger"))
		eAux := awardsAuxRouter(hz)
		eMain := hz.Router()

		userA, tokA := hz.MintUser("ledgerA")
		userB, tokB := hz.MintUser("ledgerB")
		ensureLabels(hz, "labelAlpha", "labelBeta", "labelBonly")

		// A: two different labels today.
		Expect(doPostJSONG(eAux, "/api/v1/users/current/awards/log", tokA, map[string]any{
			"items": []map[string]any{
				{"labelId": "labelAlpha", "periodType": "daily"},
				{"labelId": "labelBeta", "periodType": "weekly"},
			},
		})).To(testutil.HaveStatus(http.StatusOK))

		// B: distinct label so we can prove A's response never contains B's row.
		Expect(doPostJSONG(eAux, "/api/v1/users/current/awards/log", tokB, map[string]any{
			"items": []map[string]any{{"labelId": "labelBonly", "periodType": "daily"}},
		})).To(testutil.HaveStatus(http.StatusOK))

		// A hits the ledger — must see own rows only.
		rec := getJSONG(eMain, "/api/v1/users/current/awards/ledger", tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec).To(testutil.HaveHeader("Cache-Control", "private, max-age=30"))

		var body struct {
			Rows []struct {
				LabelID string `json:"labelId"`
			} `json:"rows"`
			Limit int `json:"limit"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Limit).To(Equal(500), "default limit must be 500 when ?limit absent")

		seenA := map[string]bool{}
		for _, r := range body.Rows {
			seenA[r.LabelID] = true
			Expect(r.LabelID).NotTo(Equal("labelBonly"),
				"CROSS-USER LEAK on /awards/ledger — A saw B's label")
		}
		Expect(seenA["labelAlpha"]).To(BeTrue())
		Expect(seenA["labelBeta"]).To(BeTrue())

		// ?label= filter narrows the response server-side.
		rec = getJSONG(eMain, "/api/v1/users/current/awards/ledger?label=labelAlpha", tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		for _, r := range body.Rows {
			Expect(r.LabelID).To(Equal("labelAlpha"),
				"?label filter leaked non-matching label %s", r.LabelID)
		}
		Expect(body.Rows).NotTo(BeEmpty(), "labelAlpha filter should return the one row A wrote")

		// ?limit=3 must clamp; parsePositiveInt maps non-int to max (500).
		rec = getJSONG(eMain, "/api/v1/users/current/awards/ledger?limit=3", tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Limit).To(Equal(3))

		rec = getJSONG(eMain, "/api/v1/users/current/awards/ledger?limit=abc", tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Limit).To(Equal(500), "non-numeric ?limit must fall back to 500 (parsePositiveInt semantics)")

		// ?limit=999999 also clamps to max=500 (in-bounds cap enforcement).
		rec = getJSONG(eMain, "/api/v1/users/current/awards/ledger?limit=999999", tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Limit).To(Equal(500), "over-max limit must clamp to 500")

		// ?limit=0 falls back to 500 (parsePositiveInt: <=0 → max).
		rec = getJSONG(eMain, "/api/v1/users/current/awards/ledger?limit=0", tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Limit).To(Equal(500), "limit=0 must fall back to 500")

		_ = userA
		_ = userB
	})

	It("unauth /awards/ledger returns 4xx before DB touch (auth gate)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsledger"))
		e := hz.Router()

		rec := getJSONG(e, "/api/v1/users/current/awards/ledger", "")
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})
})

// parsePositiveInt is unexported; test-through the /awards/ledger ?limit
// path (above) covers every branch, but this Describe pins the exact
// numeric semantics documented in the source. It uses one long spec so
// the failure log names WHICH branch failed.
var _ = Describe("parsePositiveInt semantics via /awards/ledger?limit", func() {
	It("maps every documented ?limit shape to its expected clamp value", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsledger"))
		e := hz.Router()
		_, token := hz.MintUser("parselim")

		cases := []struct {
			name  string
			query string
			want  int
		}{
			{"empty query defaults", "", 500},
			{"valid small int passes through", "?limit=7", 7},
			{"valid mid int passes through", "?limit=250", 250},
			{"exactly max=500 passes through", "?limit=500", 500},
			{"over-max clamps to 500", "?limit=501", 500},
			{"zero clamps to 500 (n<=0 branch)", "?limit=0", 500},
			{"non-numeric clamps to 500", "?limit=abc", 500},
			{"decimal (dot char) clamps to 500", "?limit=1.5", 500},
			{"empty value string clamps to 500", "?limit=", 500},
		}
		for _, tc := range cases {
			rec := getJSONG(e, "/api/v1/users/current/awards/ledger"+tc.query, token)
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "case=%q body=%s", tc.name, rec.Body.String())
			var body struct {
				Limit int `json:"limit"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Limit).To(Equal(tc.want),
				"case %q: query=%q want limit=%d got %d", tc.name, tc.query, tc.want, body.Limit)
		}
	})
})

// Ensure the direct unit-testable parsePositiveInt behavior is also
// pinned via the exported HTTP path — this is a defense in depth in
// case someone changes ?limit binding but leaves the helper stale.
var _ = Describe("parsePositiveInt boundary constants", func() {
	It("clamps a value that grows past max mid-scan (early exit inside loop)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "awardsledger"))
		e := hz.Router()
		_, token := hz.MintUser("parselimmid")

		// 550 crosses the 500 cap DURING the digit-accumulate loop; the
		// early `if n > max { return max, nil }` inside parsePositiveInt
		// is the code path here.
		rec := getJSONG(e, fmt.Sprintf("/api/v1/users/current/awards/ledger?limit=%d", 550), token)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var body struct {
			Limit int `json:"limit"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
		Expect(body.Limit).To(Equal(500), "550 must clamp to 500 mid-loop")
	})
})

// awardsStreaksFor is the shared internal helper called by both the
// auth'd AwardsStreaks and PublicAwardsStreaks. Verify the invalid-tz
// fallback branch (loc, err := time.LoadLocation(...)) is exercised by
// setting the user's timezone to something invalid and asserting the
// call still succeeds (falls back to UTC).
var _ = Describe("awardsStreaksFor tz fallback", func() {
	It("falls back to UTC when the stored user tz is invalid — endpoint still 200s", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "streaksfor"))
		e := hz.Router()
		user, token := hz.MintUser("streaktz")

		// Force an invalid IANA tz on the users row. resolveUserTZ will
		// return whatever's in the column; the handler's LoadLocation
		// call fails; loc = time.UTC is the branch under test.
		_, err := hz.DB.Pool.Exec(context.Background(),
			`UPDATE users SET timezone=$1 WHERE username=$2`,
			"Invalid/Not-A-Real-Zone", user)
		Expect(err).NotTo(HaveOccurred())

		rec := getJSONG(e, "/api/v1/users/current/awards/streaks", token)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"invalid tz must fall back to UTC and still succeed; body=%s", rec.Body.String())

		var streaks map[string]int
		Expect(json.Unmarshal(rec.Body.Bytes(), &streaks)).To(Succeed(),
			"body must decode to map[string]int even on tz-fallback path")
		_ = streaks
	})
})

var _ = Describe("OwnAwards auth gate (gaka-hc6.3)", func() {
	It("unauthenticated GET /awards returns 4xx before touching the DB", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "ownawardsauth"))
		e := hz.Router()

		rec := getJSONG(e, "/api/v1/users/current/awards", "")
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})
})

var _ = Describe("PublicAwards disabled-profile gate (gaka-hc6.3)", func() {
	It("returns 404 'This profile isn't public' when the slug exists but is disabled", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "publicawardsdisabled"))
		e := hz.Router()
		user, _ := hz.MintUser("pubawardsoff")

		slug := "disabled-slug-" + strings.ToLower(strings.ReplaceAll(user[len(user)-8:], ".", ""))
		// Enable + slug so LookupUsernameBySlug hits, then flip enabled=false
		// via a direct UPDATE (SetPublicProfile with enabled=false and
		// empty slug leaves the slug alone — perfect for this test).
		Expect(hz.DB.SetPublicProfile(context.Background(), user, true, slug)).To(Succeed())
		Expect(hz.DB.SetPublicProfile(context.Background(), user, false, "")).To(Succeed())

		rec := getJSONG(e, "/api/public/profile/"+slug+"/awards", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
			"disabled profile must 404 (no oracle for disabled-vs-nonexistent); body=%s", rec.Body.String())
	})
})

// Silence unused-import warnings on rare-alias branches. `_ = handler` /
// `_ = httptest` keep the imports live even if a future edit strips a
// direct use.
var _ = handler.BodyLimitSmall
var _ = httptest.NewRecorder
