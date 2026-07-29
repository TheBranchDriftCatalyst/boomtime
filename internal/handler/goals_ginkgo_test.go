// goals_ginkgo_test.go — ginkgo mirror of goals_test.go (gaka-wpb).
// 1:1 case map (13 stdlib TestXxx incl. subtests → 26 Its):
//   TestGoalsCRUDRoundtrip                           → goals CRUD > "POST/GET/LIST/PATCH/DELETE round-trip semantically intact"
//   TestGoalsOwnerScoping                            → goals owner scoping > "bob's id → 404 on every alice endpoint + not in list"
//   TestGoalsValidationRejects                       → goals validation > 6 Entries (unknown kind/axis/op, neg target, empty all, unknown window)
//   TestGoalsProgressCacheAndFreshness               → goals progress cache > "PATCH spec clears cache; fresh timestamp on next read"
//   TestGoalsIngestInvalidation                      → goals ingest hook > "heartbeat ingest wipes cached progress"
//   TestGoalsBatchProgressEndpoint                   → goals batch progress > "enabled included; disabled omitted"
//   TestGoalsDuplicateNameReturns409                 → goals CRUD > "duplicate name → 409"
//   TestGoalsProgressServesFromCacheWithinTTL        → goals progress cache > "two reads within TTL return same bytes and same timestamp"
//   TestGoalsBatchProgressOwnerScoping               → goals batch owner scoping > "alice's batch does NOT leak bob's id"
//   TestGoalsIngestInvalidationOwnerScoping          → goals ingest hook > "bob's ingest does NOT wipe alice's cache"
//   TestGoalsToggleHTTP                              → goals toggle > "flip; flip back; idempotent explicit set"
//   TestGoalsValidationRejectsCoversAllBranches      → goals validation extras > 10 Entries (streak/depth/not-arity/active_days/etc.)
//   TestGoalsCreateMissingFields                     → goals create guards > "empty name / whitespace / missing spec → 400"
//   TestGoalsPatchValidationAndFields                → goals patch guards > "invalid spec, whitespace name, then valid rename"
//   TestGoalsDuplicateNameOnRename409                → goals rename > "rename-to-existing → 409"
package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

const weeklyGoSpecG = `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`

// seedRollupForOwnerG is an Expect-based wrapper around hz.SeedRollup
// (defined in internal/testutil/seed.go — one shared source of truth).
func seedRollupForOwnerG(hz *testutil.Harness, owner string, day time.Time, language string, seconds int64) {
	Expect(hz.SeedRollup(owner, day, language, seconds)).To(Succeed())
}

func createGoalG(e http.Handler, token, name, spec string) string {
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": name,
		"spec": json.RawMessage(spec),
	})
	Expect(rec.Code).To(Equal(http.StatusOK), "create goal %q: body=%s", name, rec.Body.String())
	var env struct {
		Goal struct {
			ID string `json:"id"`
		} `json:"goal"`
	}
	Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
	return env.Goal.ID
}

var _ = Describe("goals CRUD (gaka-wpb)", func() {
	It("POST/GET/LIST/PATCH/DELETE round-trip preserves the spec semantically", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("goal_crud_g")

		// Create.
		postRec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
			"name":        "weekly-go-g",
			"description": "one hour a week",
			"spec":        json.RawMessage(weeklyGoSpecG),
		})
		Expect(postRec.Code).To(Equal(http.StatusOK), "POST: body=%s", postRec.Body.String())
		var postEnv struct {
			Goal struct {
				ID          string          `json:"id"`
				Name        string          `json:"name"`
				Description *string         `json:"description"`
				Spec        json.RawMessage `json:"spec"`
				Enabled     bool            `json:"enabled"`
			} `json:"goal"`
		}
		Expect(json.Unmarshal(postRec.Body.Bytes(), &postEnv)).To(Succeed())
		Expect(postEnv.Goal.ID).NotTo(BeEmpty())
		Expect(postEnv.Goal.Name).To(Equal("weekly-go-g"))
		Expect(postEnv.Goal.Enabled).To(BeTrue())
		Expect(semanticJSONDiffG(weeklyGoSpecG, string(postEnv.Goal.Spec))).To(BeEmpty(),
			"POST round-trip spec")
		id := postEnv.Goal.ID

		// GET single.
		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		Expect(getRec.Code).To(Equal(http.StatusOK), "GET: body=%s", getRec.Body.String())
		var getEnv struct {
			Goal struct {
				Spec json.RawMessage `json:"spec"`
			} `json:"goal"`
		}
		Expect(json.Unmarshal(getRec.Body.Bytes(), &getEnv)).To(Succeed())
		Expect(semanticJSONDiffG(weeklyGoSpecG, string(getEnv.Goal.Spec))).To(BeEmpty(), "GET round-trip spec")

		// GET list.
		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals", token, nil)
		Expect(listRec.Code).To(Equal(http.StatusOK))
		var listEnv struct {
			Goals []struct{ ID string } `json:"goals"`
		}
		Expect(json.Unmarshal(listRec.Body.Bytes(), &listEnv)).To(Succeed())
		Expect(listEnv.Goals).To(HaveLen(1))
		Expect(listEnv.Goals[0].ID).To(Equal(id))

		// PATCH spec.
		newSpec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`
		patchRec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
			"spec": json.RawMessage(newSpec),
		})
		Expect(patchRec.Code).To(Equal(http.StatusOK), "PATCH: body=%s", patchRec.Body.String())

		getRec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var getEnv2 struct {
			Goal struct {
				Spec json.RawMessage `json:"spec"`
			} `json:"goal"`
		}
		Expect(json.Unmarshal(getRec2.Body.Bytes(), &getEnv2)).To(Succeed())
		Expect(semanticJSONDiffG(newSpec, string(getEnv2.Goal.Spec))).To(BeEmpty(),
			"PATCH round-trip spec")

		// DELETE.
		delRec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/goals/"+id, token, nil)
		Expect(delRec.Code).To(Equal(http.StatusNoContent), "DELETE: body=%s", delRec.Body.String())

		// Second delete: 404.
		del2Rec := doJSONReqG(e, http.MethodDelete, "/api/v1/users/current/goals/"+id, token, nil)
		Expect(del2Rec.Code).To(Equal(http.StatusNotFound))
	})

	It("duplicate name returns 409 (not 500 leaked DB error)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("goal_dup_g")

		r1 := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
			"name": "same-g", "spec": json.RawMessage(weeklyGoSpecG),
		})
		Expect(r1.Code).To(Equal(http.StatusOK), "first POST: body=%s", r1.Body.String())

		r2 := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
			"name": "same-g", "spec": json.RawMessage(weeklyGoSpecG),
		})
		Expect(r2.Code).To(Equal(http.StatusConflict), "body=%s", r2.Body.String())
	})
})

var _ = Describe("goals owner scoping (no oracle)", func() {
	It("bob's id → 404 on every alice endpoint; alice's list does not include bob's id", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, aliceTok := hz.MintUser("goal_alice_g")
		_, bobTok := hz.MintUser("goal_bob_g")

		bobID := createGoalG(e, bobTok, "bob-goal-g", weeklyGoSpecG)

		cases := []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/api/v1/users/current/goals/" + bobID, nil},
			{http.MethodPatch, "/api/v1/users/current/goals/" + bobID, map[string]any{"description": "hi"}},
			{http.MethodDelete, "/api/v1/users/current/goals/" + bobID, nil},
			{http.MethodPost, "/api/v1/users/current/goals/" + bobID + "/toggle", nil},
			{http.MethodGet, "/api/v1/users/current/goals/" + bobID + "/progress", nil},
		}
		for _, c := range cases {
			rec := doJSONReqG(e, c.method, c.path, aliceTok, c.body)
			Expect(rec.Code).To(Equal(http.StatusNotFound),
				"%s %s (alice on bob's id): got %d body=%s — want 404 (no oracle)",
				c.method, c.path, rec.Code, rec.Body.String())
		}

		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals", aliceTok, nil)
		Expect(listRec.Code).To(Equal(http.StatusOK), "LIST alice body=%s", listRec.Body.String())
		Expect(listRec.Body.String()).NotTo(ContainSubstring(bobID),
			"alice's list leaked bob's id: %s", listRec.Body.String())
	})
})

var _ = Describe("goals validation (ValidateSpec branches)", func() {
	DescribeTable("bad spec → 400",
		func(spec string) {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("goal_reject_g_" + strings.ReplaceAll(spec[:min(10, len(spec))], `"`, ""))
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
				"name": "n_reject",
				"spec": json.RawMessage(spec),
			})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "body=%s", rec.Body.String())
		},
		Entry("unknown kind", `{"kind":"stapler"}`),
		Entry("unknown axis", `{"kind":"time","axis":"chickens","op":">=","target_seconds":1,"window":"week"}`),
		Entry("unknown op", `{"kind":"time","axis":"language","op":"!=","target_seconds":1,"window":"week"}`),
		Entry("negative target", `{"kind":"time","axis":"language","op":">=","target_seconds":-5,"window":"week"}`),
		Entry("empty all", `{"kind":"all","of":[]}`),
		Entry("unknown window", `{"kind":"time","axis":"language","op":">=","target_seconds":1,"window":"decade"}`),
	)
})

var _ = Describe("goals validation extras (all branches)", func() {
	// Build a depth-6 spec (over the depth-5 cap).
	deep := `{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"}`
	for i := 0; i < 6; i++ {
		deep = `{"kind":"all","of":[` + deep + `]}`
	}

	DescribeTable("bad spec → 400 with error hint in body",
		func(spec string) {
			hz := testutil.NewHarness(GinkgoT())
			e := hz.Router()
			_, token := hz.MintUser("goal_reject_full_g")
			rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
				"name": "vr_full",
				"spec": json.RawMessage(spec),
			})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), "body=%s", rec.Body.String())
			Expect(len(rec.Body.String())).To(BeNumerically(">=", 5),
				"body too short (no error hint): %q", rec.Body.String())
		},
		Entry("streak missing condition", `{"kind":"streak","min_days":3}`),
		Entry("streak min_days negative", `{"kind":"streak","min_days":-1,"condition":{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"day"}}`),
		Entry("streak min_days too big", `{"kind":"streak","min_days":9999,"condition":{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"day"}}`),
		Entry("empty any", `{"kind":"any","of":[]}`),
		Entry("not wrong arity 0", `{"kind":"not","of":[]}`),
		Entry("not wrong arity 2", `{"kind":"not","of":[{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},{"kind":"time","axis":"project","value":null,"op":">=","target_seconds":1,"window":"week"}]}`),
		Entry("active_days negative n", `{"kind":"active_days","op":">=","n":-1,"window":"week"}`),
		Entry("active_days invalid window", `{"kind":"active_days","op":">=","n":1,"window":"day"}`),
		Entry("depth cap", deep),
		Entry("nested bad axis inside all", `{"kind":"all","of":[{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},{"kind":"time","axis":"chicken","value":null,"op":">=","target_seconds":1,"window":"week"}]}`),
	)
})

var _ = Describe("goals progress cache", func() {
	It("PATCH spec clears cache; fresh timestamp on next read", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("goal_progcache_g")

		seedRollupForOwnerG(hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 4000)
		id := createGoalG(e, token, "prog-g", weeklyGoSpecG)

		rec1 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
		Expect(rec1.Code).To(Equal(http.StatusOK), "progress 1: body=%s", rec1.Body.String())

		var prog1 struct {
			Hit      bool    `json:"hit"`
			Progress float64 `json:"progress"`
		}
		Expect(json.Unmarshal(rec1.Body.Bytes(), &prog1)).To(Succeed())
		Expect(prog1.Hit).To(BeTrue())
		Expect(prog1.Progress).To(Equal(float64(1)))

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterFirst struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &afterFirst)
		Expect(afterFirst.Goal.LastEvaluatedAt).NotTo(BeNil(), "first GET /progress did not populate cache")
		firstTs := *afterFirst.Goal.LastEvaluatedAt

		// PATCH spec — cache must clear.
		newSpec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`
		patchRec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
			"spec": json.RawMessage(newSpec),
		})
		Expect(patchRec.Code).To(Equal(http.StatusOK), "PATCH: body=%s", patchRec.Body.String())

		getAfterPatch := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterPatch struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getAfterPatch.Body.Bytes(), &afterPatch)
		Expect(afterPatch.Goal.LastEvaluatedAt).To(BeNil(),
			"PATCH spec did NOT clear cache: %v", afterPatch.Goal.LastEvaluatedAt)

		time.Sleep(2 * time.Millisecond)
		rec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
		Expect(rec2.Code).To(Equal(http.StatusOK), "progress 2: body=%s", rec2.Body.String())
		var prog2 struct {
			Hit bool `json:"hit"`
		}
		_ = json.Unmarshal(rec2.Body.Bytes(), &prog2)
		Expect(prog2.Hit).To(BeFalse(), "progress 2 should miss under new higher target")

		getRec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterSecond struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec2.Body.Bytes(), &afterSecond)
		Expect(afterSecond.Goal.LastEvaluatedAt).NotTo(BeNil())
		Expect(afterSecond.Goal.LastEvaluatedAt.After(firstTs)).To(BeTrue(),
			"fresh timestamp expected: got %v want > %v", afterSecond.Goal.LastEvaluatedAt, firstTs)
	})

	It("two reads within TTL return same bytes and same timestamp (cache actually serves)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("goal_cachehit_g")

		seedRollupForOwnerG(hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)
		id := createGoalG(e, token, "cachehit-g", weeklyGoSpecG)

		rec1 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
		Expect(rec1.Code).To(Equal(http.StatusOK))

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterFirst struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &afterFirst)
		Expect(afterFirst.Goal.LastEvaluatedAt).NotTo(BeNil())
		firstTs := *afterFirst.Goal.LastEvaluatedAt

		time.Sleep(5 * time.Millisecond)
		rec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
		Expect(rec2.Code).To(Equal(http.StatusOK))
		Expect(rec1.Body.String()).To(Equal(rec2.Body.String()),
			"body diverged inside TTL")

		getRec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterSecond struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec2.Body.Bytes(), &afterSecond)
		Expect(afterSecond.Goal.LastEvaluatedAt).NotTo(BeNil(), "cache row wiped (should have served from cache)")
		Expect(afterSecond.Goal.LastEvaluatedAt.Equal(firstTs)).To(BeTrue(),
			"last_evaluated_at changed on cache-hit read: was %v, now %v",
			firstTs, *afterSecond.Goal.LastEvaluatedAt)
	})
})

var _ = Describe("goals ingest invalidation hook", func() {
	It("heartbeat ingest wipes cached progress for the ingesting owner", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("goal_ingest_g")

		seedRollupForOwnerG(hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 1000)
		id := createGoalG(e, token, "ingest-g", weeklyGoSpecG)

		rec1 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
		Expect(rec1.Code).To(Equal(http.StatusOK), "progress 1: body=%s", rec1.Body.String())

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterFirst struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &afterFirst)
		Expect(afterFirst.Goal.LastEvaluatedAt).NotTo(BeNil(), "first progress read did not populate cache")

		now := float64(time.Now().Unix())
		ingestBody := []map[string]any{{
			"time":       now,
			"entity":     "a.go",
			"type":       "file",
			"project":    "P",
			"language":   "Go",
			"user_agent": "wakatime/1 (Linux) go/1 vscode",
		}}
		ingRec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/heartbeats.bulk", token, ingestBody)
		Expect(ingRec.Code).To(Equal(http.StatusAccepted), "heartbeat ingest: body=%s", ingRec.Body.String())

		getRec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var afterIngest struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec2.Body.Bytes(), &afterIngest)
		Expect(afterIngest.Goal.LastEvaluatedAt).To(BeNil(),
			"heartbeat ingest did NOT invalidate cache: last_evaluated_at still %v",
			afterIngest.Goal.LastEvaluatedAt)
	})

	It("bob's ingest does NOT wipe alice's cache (invalidation is owner-scoped)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceOwner, aliceTok := hz.MintUser("goal_ing_scope_a_g")
		_, bobTok := hz.MintUser("goal_ing_scope_b_g")

		seedRollupForOwnerG(hz, aliceOwner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)
		aID := createGoalG(e, aliceTok, "a-scoped-g", weeklyGoSpecG)

		r := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+aID+"/progress", aliceTok, nil)
		Expect(r.Code).To(Equal(http.StatusOK), "alice progress: body=%s", r.Body.String())

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+aID, aliceTok, nil)
		var afterAliceRead struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &afterAliceRead)
		Expect(afterAliceRead.Goal.LastEvaluatedAt).NotTo(BeNil(), "alice cache didn't land")
		aliceTsBefore := *afterAliceRead.Goal.LastEvaluatedAt

		now := float64(time.Now().Unix())
		ingestBody := []map[string]any{{
			"time":       now,
			"entity":     "b.py",
			"type":       "file",
			"project":    "P",
			"language":   "Python",
			"user_agent": "wakatime/1 (Linux) go/1 vscode",
		}}
		ingRec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/heartbeats.bulk", bobTok, ingestBody)
		Expect(ingRec.Code).To(Equal(http.StatusAccepted), "bob ingest: body=%s", ingRec.Body.String())

		getRec2 := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+aID, aliceTok, nil)
		var afterBobIngest struct {
			Goal struct {
				LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec2.Body.Bytes(), &afterBobIngest)
		Expect(afterBobIngest.Goal.LastEvaluatedAt).NotTo(BeNil(),
			"bob's ingest WIPED alice's cache — invalidation is not owner-scoped")
		Expect(afterBobIngest.Goal.LastEvaluatedAt.Equal(aliceTsBefore)).To(BeTrue(),
			"alice's cache timestamp drifted after bob's ingest: was %v, now %v",
			aliceTsBefore, *afterBobIngest.Goal.LastEvaluatedAt)
	})
})

var _ = Describe("goals batched progress", func() {
	It("returns map keyed by id; disabled goals are omitted", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, token := hz.MintUser("goal_batch_g")

		seedRollupForOwnerG(hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)

		id1 := createGoalG(e, token, "g-enabled-g", weeklyGoSpecG)
		id2 := createGoalG(e, token, "g-disabled-g", weeklyGoSpecG)
		tglRec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals/"+id2+"/toggle", token, map[string]any{
			"enabled": false,
		})
		Expect(tglRec.Code).To(Equal(http.StatusOK), "toggle: body=%s", tglRec.Body.String())

		batchRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/progress", token, nil)
		Expect(batchRec.Code).To(Equal(http.StatusOK), "batch progress: body=%s", batchRec.Body.String())
		var env struct {
			Progress map[string]struct {
				Hit bool `json:"hit"`
			} `json:"progress"`
		}
		Expect(json.Unmarshal(batchRec.Body.Bytes(), &env)).To(Succeed())
		Expect(env.Progress).To(HaveKey(id1), "enabled goal missing")
		Expect(env.Progress).NotTo(HaveKey(id2), "disabled goal appears")
	})

	It("alice's batch never leaks bob's id (owner-scoped)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		aliceOwner, aliceTok := hz.MintUser("goal_batch_a_g")
		bobOwner, bobTok := hz.MintUser("goal_batch_b_g")

		seedRollupForOwnerG(hz, aliceOwner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)
		seedRollupForOwnerG(hz, bobOwner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)

		aID := createGoalG(e, aliceTok, "a-batch-g", weeklyGoSpecG)
		bID := createGoalG(e, bobTok, "b-batch-g", weeklyGoSpecG)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/progress", aliceTok, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())

		var env struct {
			Progress map[string]any `json:"progress"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		Expect(env.Progress).NotTo(HaveKey(bID),
			"alice's batch LEAKED bob's goal id %s", bID)
		Expect(env.Progress).To(HaveKey(aID), "alice's own goal missing: %+v", env.Progress)
		Expect(env.Progress).To(HaveLen(1))
	})
})

var _ = Describe("goals toggle endpoint", func() {
	It("flips off then on; explicit set is idempotent", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("goal_toggle_http_g")

		id := createGoalG(e, token, "toggle-http-g", weeklyGoSpecG)

		// Flip 1: no body, expect enabled=false.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token, nil)
		Expect(rec.Code).To(Equal(http.StatusOK), "flip 1: body=%s", rec.Body.String())
		var env struct {
			Enabled bool `json:"enabled"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		Expect(env.Enabled).To(BeFalse())

		// GET confirms DB state.
		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var got struct {
			Goal struct {
				Enabled bool `json:"enabled"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &got)
		Expect(got.Goal.Enabled).To(BeFalse(), "response and DB desynced")

		// Flip 2: back to true.
		rec2 := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token, nil)
		var env2 struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(rec2.Body.Bytes(), &env2)
		Expect(env2.Enabled).To(BeTrue())

		// Idempotent set true=true.
		rec3 := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token, map[string]any{
			"enabled": true,
		})
		Expect(rec3.Code).To(Equal(http.StatusOK), "idempotent set: body=%s", rec3.Body.String())
		var env3 struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(rec3.Body.Bytes(), &env3)
		Expect(env3.Enabled).To(BeTrue())
	})
})

var _ = Describe("goals create guards (shape-level)", func() {
	It("empty name / whitespace / missing spec → 400", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("goal_missing_fields_g")

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
			"name": "",
			"spec": json.RawMessage(weeklyGoSpecG),
		})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "empty name: body=%s", rec.Body.String())

		rec = doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
			"name": "   ",
			"spec": json.RawMessage(weeklyGoSpecG),
		})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "whitespace: body=%s", rec.Body.String())

		rec = doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
			"name": "no-spec-g",
		})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "missing spec: body=%s", rec.Body.String())
	})
})

var _ = Describe("goals PATCH guards", func() {
	It("PATCH invalid spec → 400; whitespace-only name → 400; valid rename persists", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("goal_patch_val_g")

		id := createGoalG(e, token, "patch-val-g", weeklyGoSpecG)

		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
			"spec": json.RawMessage(`{"kind":"time","axis":"CHICKEN","op":">=","target_seconds":1,"window":"week"}`),
		})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "bad spec: body=%s", rec.Body.String())

		rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
			"name": "   ",
		})
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "empty name: body=%s", rec.Body.String())

		rec = doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
			"name": "renamed-g",
		})
		Expect(rec.Code).To(Equal(http.StatusOK), "valid rename: body=%s", rec.Body.String())

		getRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
		var got struct {
			Goal struct {
				Name string `json:"name"`
			} `json:"goal"`
		}
		_ = json.Unmarshal(getRec.Body.Bytes(), &got)
		Expect(got.Goal.Name).To(Equal("renamed-g"))
	})
})

var _ = Describe("goals rename collision", func() {
	It("rename-to-existing (same owner) returns 409, not 500", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("goal_rename_dup_g")

		_ = createGoalG(e, token, "existing-name-g", weeklyGoSpecG)
		id2 := createGoalG(e, token, "second-name-g", weeklyGoSpecG)

		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/users/current/goals/"+id2, token, map[string]any{
			"name": "existing-name-g",
		})
		Expect(rec.Code).To(Equal(http.StatusConflict), "body=%s", rec.Body.String())
	})
})

// min — Go 1.21+ builtin; local shim if the target Go tests get compiled on older.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
