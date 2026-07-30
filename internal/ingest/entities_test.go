// entities_test.go — gaka-d6x.handler: cover ListEntitiesByType + RedactEntities.
// Named invariants:
//
//	"missing/bad type → 400" — the ?type= whitelist rejects the empty
//	string, an unknown value, and case variants. Server never runs a query
//	before the whitelist gate.
//
//	"cross-user isolation on list" — alice's file entities never appear in
//	bob's list even when both users have overlapping filenames. Owner is
//	resolved from the token; the SQL filter is per-owner.
//
//	"redact requires confirm=redact-entities" — without the sentinel query
//	param, an authed POST returns 400 and the target row is NOT scrubbed
//	(entity column unchanged).
//
//	"redact refuses empty batch and oversize batch" — 0 or >500 entities is
//	400; the DB layer is not called.
package ingest_test

import (
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Entity Explorer (gaka-d6x.handler)", func() {
	It("unauthenticated → 4xx (no DB touch)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/heartbeats/entities?type=file", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400))
		Expect(rec.Code).To(BeNumerically("<", 500))
	})

	It("normalizes limit: negative and oversize both return 200", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("ent_lim")

		// limit < 1 → falls back to default.
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/heartbeats/entities?type=file&limit=0", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "limit=0: %s", rec.Body.String())

		// limit > entityListMaxLimit → clamped to cap.
		rec = doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/heartbeats/entities?type=file&limit=99999", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "limit=99999: %s", rec.Body.String())
	})

	It("rejects an absent or non-whitelisted ?type with 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("entlist_type")

		// Missing type.
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/heartbeats/entities", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"missing type: body=%s", rec.Body.String())

		// Unknown type.
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/users/current/heartbeats/entities?type=chicken", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"unknown type: body=%s", rec.Body.String())

		// Wrong case (whitelist is exact-lowercase).
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/users/current/heartbeats/entities?type=FILE", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"uppercased type must not match: body=%s", rec.Body.String())
	})

	It("lists a user's file entities and never leaks a second user's rows", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("entlist_a")
		bobUser, bobTok := hz.MintUser("entlist_b")

		base := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
		// Overlapping filenames per owner. If the query weren't owner-scoped
		// the totals would double.
		hz.Seeder(aliceUser).Projects("alpha").
			Seed(testutil.HB{Project: "alpha", TS: base, Entity: "shared/main.go", Ty: "file"}).
			Seed(testutil.HB{Project: "alpha", TS: base.Add(time.Minute), Entity: "alice-only.go", Ty: "file"})
		hz.Seeder(bobUser).Projects("beta").
			Seed(testutil.HB{Project: "beta", TS: base, Entity: "shared/main.go", Ty: "file"}).
			Seed(testutil.HB{Project: "beta", TS: base.Add(time.Minute), Entity: "bob-only.go", Ty: "file"})

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/heartbeats/entities?type=file", aliceTok, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var env struct {
			Entities []struct {
				Entity string `json:"entity"`
				Count  int64  `json:"count"`
			} `json:"entities"`
			Truncated bool `json:"truncated"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())

		var sawShared, sawAliceOnly bool
		for _, r := range env.Entities {
			if r.Entity == "shared/main.go" {
				sawShared = true
				// Bob has one shared/main.go too. If the list weren't
				// owner-scoped this count would be 2, not 1.
				Expect(r.Count).To(BeEquivalentTo(1),
					"cross-owner leak: shared/main.go count is %d (want 1)", r.Count)
			}
			if r.Entity == "alice-only.go" {
				sawAliceOnly = true
			}
		}
		Expect(sawShared).To(BeTrue(), "shared/main.go missing from alice's list")
		Expect(sawAliceOnly).To(BeTrue(), "alice-only.go missing")

		// The response body must NEVER contain the bob-only entity name.
		Expect(rec.Body.String()).NotTo(ContainSubstring("bob-only.go"),
			"leak: alice's list contained bob's exclusive entity")
		_ = bobTok
	})

	It("redact requires ?confirm=redact-entities; without it, DB row is untouched", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("entred_a")

		base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		hz.Seeder(aliceUser).Projects("alpha").
			Seed(testutil.HB{Project: "alpha", TS: base, Entity: "sensitive.go", Ty: "file"})

		// No confirm param → 400.
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/heartbeats/entities/redact",
			aliceTok, map[string]any{"ty": "file", "entities": []string{"sensitive.go"}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"missing confirm: body=%s", rec.Body.String())

		// The DB row must still say "sensitive.go" (blanking would set it to '').
		listRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/heartbeats/entities?type=file", aliceTok, nil)
		Expect(listRec).To(testutil.HaveStatus(http.StatusOK))
		Expect(listRec.Body.String()).To(ContainSubstring("sensitive.go"),
			"unconfirmed redact should not have blanked the entity: body=%s",
			listRec.Body.String())
	})

	It("redact with the correct sentinel actually scrubs the entity column", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceUser, aliceTok := hz.MintUser("entred_ok")

		base := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
		hz.Seeder(aliceUser).Projects("alpha").
			Seed(testutil.HB{Project: "alpha", TS: base, Entity: "purge-me.go", Ty: "file"})

		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/heartbeats/entities/redact?confirm=redact-entities",
			aliceTok, map[string]any{"ty": "file", "entities": []string{"purge-me.go"}})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		var env struct {
			Redacted int64 `json:"redacted"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		Expect(env.Redacted).To(BeNumerically(">=", 1),
			"redacted count should be >=1 for a single-file batch; got %d body=%s",
			env.Redacted, rec.Body.String())

		// The list view excludes blanked (entity='') rows.
		listRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/heartbeats/entities?type=file", aliceTok, nil)
		Expect(listRec.Body.String()).NotTo(ContainSubstring("purge-me.go"),
			"post-redact list still contains the scrubbed entity: %s",
			listRec.Body.String())
	})

	It("redact validates the type + batch bounds (400 before DB touch)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("entred_val")

		// Bad ty.
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/heartbeats/entities/redact?confirm=redact-entities",
			token, map[string]any{"ty": "chicken", "entities": []string{"x"}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// Empty batch.
		rec = doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/heartbeats/entities/redact?confirm=redact-entities",
			token, map[string]any{"ty": "file", "entities": []string{}})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))

		// Oversize batch (>500).
		big := make([]string, 501)
		for i := range big {
			big[i] = "f"
		}
		rec = doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/heartbeats/entities/redact?confirm=redact-entities",
			token, map[string]any{"ty": "file", "entities": big})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"501-item batch must be rejected; body=%s", rec.Body.String())
	})
})
