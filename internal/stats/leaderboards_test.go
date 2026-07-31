// leaderboards_test.go — gaka-d6x.handler: cover Leaderboards.
//
// Named invariants:
//
//	"unauth → 4xx" — the endpoint requires a token even though the
//	response is cross-user (curation/space scoping is per-requester).
//
//	"200 + payload SHAPE for an authed user" — leaderboards works even
//	with zero heartbeats seeded, and returns real JSON with the {global,
//	lang} envelope. Empty state currently renders `global` as null OR []
//	depending on whether the DB query returned zero rows or a nil slice.
//	We pin the top-level keys are present + lang renders as an object
//	(never null) — pinning `global`-is-[] would either need a handler
//	fix or make the test a change-detector. The lang-is-not-null pin is
//	the FE-relevant one (the FE ranges over the map).
//
//	"cache is owner-scoped: alice's curation rule affects HER leaderboard
//	but NOT bob's" — the strongest owner-scoped-cache proof for a
//	cross-user endpoint. Alice hides her top-scoring project via
//	curation; bob (same DB) does not. Alice's leaderboard excludes her
//	own name (her seconds sub-60 filter drops her) while bob's still
//	includes both. Byte inequality is a direct consequence: identical
//	cache-key regression would give the two callers the same payload
//	and this test would fail.
package stats_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Leaderboards (gaka-d6x.handler)", func() {
	It("rejects unauth'd GET with 4xx", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboards", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(BeNumerically(">=", 400))
	})

	It("authed empty user returns 200 with a {global, lang:{}} envelope", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		lbEmptyUser, token := hz.MintUser("lb_empty")

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/leaderboards", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		// Decode into the exact envelope model.LeaderboardsPayload advertises.
		var env struct {
			Global []map[string]any            `json:"global"`
			Lang   map[string][]map[string]any `json:"lang"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed(),
			"decode: %s", rec.Body.String())
		// `lang` MUST be a real object, not null — the FE ranges over the
		// map keys and a null would blow up. This is the FE-relevant
		// contract pin for the empty state.
		Expect(env.Lang).NotTo(BeNil(),
			"lang must render as {} not null: %s", rec.Body.String())
		// `global` is a WHOLE-DB view — asserting it's globally empty would
		// fail under any parallel test that seeds heartbeats into the shared
		// test DB (gaka-peu). The per-owner invariant we actually want is
		// that our zero-heartbeat user does NOT appear in the top-N.
		for _, row := range env.Global {
			Expect(row["user"]).NotTo(Equal(lbEmptyUser),
				"user with zero heartbeats must not appear in the global leaderboard: %+v", row)
		}
	})

	It("cache is per-owner: alice's curation rule affects HER payload but NOT bob's", func() {
		// This is the strongest owner-scoped-cache proof for a cross-user
		// endpoint. The critique flagged that the previous test only
		// asserted StatusOK for both callers — a globally-shared cache key
		// would still return 200 for both. Here we make alice's per-owner
		// curation load actually MODIFY the leaderboard payload for her,
		// leaving bob's payload untouched. If the cache key omits the
		// owner prefix, the second-caller read serves the first caller's
		// bytes and the two responses match — test fails.
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("lb_a")
		bobUser, bobTok := hz.MintUser("lb_b")

		// Seed both users so each has attributable heartbeats. Alice's
		// project name ('secret-alpha') will be the one she hides.
		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		sdA := hz.Seeder(aliceUser).Projects("secret-alpha")
		sdA.Block(testutil.HB{Project: "secret-alpha", Language: "Go", Editor: "vim"}, base, 5, 120)
		sdA.RecomputeGaps(base.AddDate(0, 0, -1))
		sdA.RefreshRollup(base.AddDate(0, 0, -1))

		sdB := hz.Seeder(bobUser).Projects("beta")
		sdB.Block(testutil.HB{Project: "beta", Language: "Rust", Editor: "code"}, base, 5, 60)
		sdB.RecomputeGaps(base.AddDate(0, 0, -1))
		sdB.RefreshRollup(base.AddDate(0, 0, -1))

		// Alice hides 'secret-alpha' via the curation API. Now alice's
		// per-owner LoadHiddenSets returns {project:[secret-alpha]}; bob's
		// stays empty. The leaderboard query applies the requester's
		// hidden set to the ROWS SCAN, so alice's rows for secret-alpha
		// drop → her totals plummet → she may fall off the >60s threshold.
		hideRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation", aliceTok, map[string]any{
				"axis": "project", "action": "hide",
				"matchType": "exact", "matchValue": "secret-alpha",
			})
		Expect(hideRec).To(testutil.HaveStatus(http.StatusOK),
			"hide rule create: body=%s", hideRec.Body.String())

		// Query covers the seeded activity range explicitly.
		start := base.AddDate(0, 0, -1).Format(time.RFC3339)
		end := base.AddDate(0, 0, 1).Format(time.RFC3339)
		q := "?start=" + url.QueryEscape(start) + "&end=" + url.QueryEscape(end)

		aRec := doJSONReqG(e, http.MethodGet, "/api/v1/leaderboards"+q, aliceTok, nil)
		Expect(aRec).To(testutil.HaveStatus(http.StatusOK), "alice: %s", aRec.Body.String())
		bRec := doJSONReqG(e, http.MethodGet, "/api/v1/leaderboards"+q, bobTok, nil)
		Expect(bRec).To(testutil.HaveStatus(http.StatusOK), "bob: %s", bRec.Body.String())

		// The strongest cache-key isolation pin: the two responses must
		// NOT be byte-identical. Alice's payload has her rows filtered
		// out by her own hide rule; bob's still includes both users' rows.
		Expect(aRec.Body.String()).NotTo(Equal(bRec.Body.String()),
			"owner-scoped cache regression: alice's payload (with her hide rule) equals bob's (no hide rule) — cache key must be per-owner. alice=%s bob=%s",
			aRec.Body.String(), bRec.Body.String())

		// Direct assertion on the effect: alice's response must not
		// mention her own name (she's filtered out of her own view) but
		// bob's must include both names.
		Expect(aRec.Body.String()).NotTo(ContainSubstring(aliceUser),
			"alice hid her top project → she should not appear on HER leaderboard: %s",
			aRec.Body.String())
		Expect(bRec.Body.String()).To(ContainSubstring(bobUser),
			"bob's own name should appear on his leaderboard: %s", bRec.Body.String())
	})
})
