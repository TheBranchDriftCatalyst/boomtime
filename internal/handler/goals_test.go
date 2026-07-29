// goals_test.go — integration tests for the goals HTTP handlers
// (gaka-wpb).
//
// Non-tautology anchors:
//
//   - CRUD ROUND-TRIP: POST → GET returns the same spec (semantic
//     JSON equality — Postgres JSONB doesn't preserve object key
//     order but MUST preserve array order + all values). PATCH
//     narrows the spec and the read-back reflects the new one. DELETE
//     removes.
//
//   - Owner scoping (no oracle): alice CANNOT fetch / edit / delete /
//     toggle / read-progress on bob's goal. Every call returns 404.
//     A leak here would surface as any of these returning 200/204.
//
//   - CACHE FRESHNESS: two GETs on /progress inside the TTL both hit
//     the cache (same body, same last_evaluated_at seen through GET
//     /goals/:id). PATCHing the spec then reading /progress produces
//     a NEW timestamp — the invariant that spec-change nulls the
//     cache is the load-bearing bit.
//
//   - INGEST INVALIDATION: seed a goal, read progress (cache lands),
//     ingest one heartbeat, read progress again — the timestamp must
//     have advanced (the ingest hook cleared the cache, next read
//     recomputed). Without the hook, the read would still hit the
//     60s-old cache and produce the same timestamp.
//
//   - VALIDATION: create with a spec that violates each ValidateSpec
//     branch — every one must land 400 with an error message.
package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// simple weekly-Go-goal spec used across tests. Kept as a const so a
// diff at the top of the file surfaces any accidental reformat.
const weeklyGoSpec = `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`


// seedRollupForOwner inserts one hb_rollup_daily row so leaf-time
// evaluations return a non-zero current. Kept minimal (one row) so
// the test stays fast — the DB layer + evaluator layer already cover
// wide-seed correctness.
// seedRollupForOwner is a Fatal-on-error wrapper around hz.SeedRollup
// (defined in internal/testutil/seed.go — one shared source of truth).
// Kept as a thin file-local shim so the stdlib TestXxx callers stay
// compact; the ginkgo mirror has its own Expect-based wrapper.
func seedRollupForOwner(t *testing.T, hz *testutil.Harness, owner string, day time.Time, language string, seconds int64) {
	t.Helper()
	if err := hz.SeedRollup(owner, day, language, seconds); err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
}

// createGoal is a shorthand that POSTs a goal and returns its id.
func createGoal(t *testing.T, e http.Handler, token, name, spec string) string {
	t.Helper()
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": name,
		"spec": json.RawMessage(spec),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create goal %q: status %d body=%s", name, rec.Code, rec.Body.String())
	}
	var env struct {
		Goal struct {
			ID string `json:"id"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode create response: %v body=%s", err, rec.Body.String())
	}
	return env.Goal.ID
}

// TestGoalsCRUDRoundtrip walks the full lifecycle and asserts the
// spec survives a POST → GET → PATCH → GET cycle semantically intact.
func TestGoalsCRUDRoundtrip(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_crud")

	// Create.
	postRec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name":        "weekly-go",
		"description": "one hour a week",
		"spec":        json.RawMessage(weeklyGoSpec),
	})
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST: status %d body=%s", postRec.Code, postRec.Body.String())
	}
	var postEnv struct {
		Goal struct {
			ID          string          `json:"id"`
			Name        string          `json:"name"`
			Description *string         `json:"description"`
			Spec        json.RawMessage `json:"spec"`
			Enabled     bool            `json:"enabled"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(postRec.Body.Bytes(), &postEnv); err != nil {
		t.Fatalf("decode POST: %v", err)
	}
	if postEnv.Goal.ID == "" || postEnv.Goal.Name != "weekly-go" || !postEnv.Goal.Enabled {
		t.Fatalf("POST response invariants: %+v", postEnv.Goal)
	}
	if diff := semanticJSONDiff(weeklyGoSpec, string(postEnv.Goal.Spec)); diff != "" {
		t.Errorf("POST round-trip spec: %s", diff)
	}
	id := postEnv.Goal.ID

	// GET single.
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET: status %d body=%s", getRec.Code, getRec.Body.String())
	}
	var getEnv struct {
		Goal struct {
			Spec json.RawMessage `json:"spec"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &getEnv); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if diff := semanticJSONDiff(weeklyGoSpec, string(getEnv.Goal.Spec)); diff != "" {
		t.Errorf("GET round-trip spec: %s", diff)
	}

	// GET list.
	listRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals", token, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("LIST: status %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listEnv struct {
		Goals []struct{ ID string } `json:"goals"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listEnv); err != nil {
		t.Fatalf("decode LIST: %v", err)
	}
	if len(listEnv.Goals) != 1 || listEnv.Goals[0].ID != id {
		t.Errorf("LIST goal ids: %+v (want [%s])", listEnv.Goals, id)
	}

	// PATCH spec: narrow the target. Read-back must reflect it.
	newSpec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`
	patchRec := doJSONReq(t, e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
		"spec": json.RawMessage(newSpec),
	})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH: status %d body=%s", patchRec.Code, patchRec.Body.String())
	}
	// Read back.
	getRec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var getEnv2 struct {
		Goal struct {
			Spec json.RawMessage `json:"spec"`
		} `json:"goal"`
	}
	if err := json.Unmarshal(getRec2.Body.Bytes(), &getEnv2); err != nil {
		t.Fatalf("decode GET after PATCH: %v", err)
	}
	if diff := semanticJSONDiff(newSpec, string(getEnv2.Goal.Spec)); diff != "" {
		t.Errorf("PATCH round-trip spec: %s", diff)
	}

	// DELETE.
	delRec := doJSONReq(t, e, http.MethodDelete, "/api/v1/users/current/goals/"+id, token, nil)
	if delRec.Code != http.StatusNoContent {
		t.Errorf("DELETE: status %d body=%s", delRec.Code, delRec.Body.String())
	}
	// Second delete: 404 (idempotent-in-effect).
	del2Rec := doJSONReq(t, e, http.MethodDelete, "/api/v1/users/current/goals/"+id, token, nil)
	if del2Rec.Code != http.StatusNotFound {
		t.Errorf("second DELETE: status %d, want 404", del2Rec.Code)
	}
}

// TestGoalsOwnerScoping is the load-bearing no-oracle contract: bob's
// goal id returns 404 on every alice endpoint, never a
// differentiating status. A single leak would prove the handler
// filter drifted from the DB filter.
func TestGoalsOwnerScoping(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, aliceTok := hz.MintUser("goal_alice")
	_, bobTok := hz.MintUser("goal_bob")

	bobID := createGoal(t, e, bobTok, "bob-goal", weeklyGoSpec)

	// Every one of these must be 404 for alice.
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
		rec := doJSONReq(t, e, c.method, c.path, aliceTok, c.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s (alice on bob's id): status %d body=%s — want 404 (no oracle)",
				c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	// Alice's list should not include bob's goal.
	listRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals", aliceTok, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("LIST alice: %d %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), bobID) {
		t.Errorf("alice's list leaked bob's id: %s", listRec.Body.String())
	}
}

// TestGoalsValidationRejects covers each ValidateSpec branch as a POST
// — regressions in the wire-level validator surface here.
func TestGoalsValidationRejects(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_reject")

	bad := map[string]string{
		"unknown kind":     `{"kind":"stapler"}`,
		"unknown axis":     `{"kind":"time","axis":"chickens","op":">=","target_seconds":1,"window":"week"}`,
		"unknown op":       `{"kind":"time","axis":"language","op":"!=","target_seconds":1,"window":"week"}`,
		"negative target":  `{"kind":"time","axis":"language","op":">=","target_seconds":-5,"window":"week"}`,
		"empty all":        `{"kind":"all","of":[]}`,
		"unknown window":   `{"kind":"time","axis":"language","op":">=","target_seconds":1,"window":"decade"}`,
	}
	for name, spec := range bad {
		t.Run(name, func(t *testing.T) {
			rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
				"name": "n_" + name,
				"spec": json.RawMessage(spec),
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestGoalsProgressCacheAndFreshness proves:
//   1. The first GET /progress computes and returns the payload.
//   2. A PATCH to the SPEC clears the cache — the next read is fresh
//      (last_evaluated_at moved forward vs before).
func TestGoalsProgressCacheAndFreshness(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	owner, token := hz.MintUser("goal_progcache")

	// Seed enough Go time to hit target 3600 in the week window.
	seedRollupForOwner(t, hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 4000)

	id := createGoal(t, e, token, "prog", weeklyGoSpec)

	// First read: compute + cache.
	rec1 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("progress GET 1: status %d body=%s", rec1.Code, rec1.Body.String())
	}
	var prog1 struct {
		Hit           bool    `json:"hit"`
		Progress      float64 `json:"progress"`
		SubConditions []any   `json:"sub_conditions"`
	}
	if err := json.Unmarshal(rec1.Body.Bytes(), &prog1); err != nil {
		t.Fatalf("decode progress 1: %v", err)
	}
	if !prog1.Hit || prog1.Progress != 1 {
		t.Errorf("progress 1: hit=%v progress=%v, want (true, 1) (seeded 4000 vs target 3600)",
			prog1.Hit, prog1.Progress)
	}

	// Read the goal row directly to capture last_evaluated_at.
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterFirst struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &afterFirst)
	if afterFirst.Goal.LastEvaluatedAt == nil {
		t.Fatalf("first GET /progress did not populate lastEvaluatedAt (cache not written)")
	}
	firstTs := *afterFirst.Goal.LastEvaluatedAt

	// PATCH spec — cache must clear.
	newSpec := `{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":7200,"window":"week"}`
	patchRec := doJSONReq(t, e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
		"spec": json.RawMessage(newSpec),
	})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH: status %d body=%s", patchRec.Code, patchRec.Body.String())
	}
	getAfterPatch := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterPatch struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getAfterPatch.Body.Bytes(), &afterPatch)
	if afterPatch.Goal.LastEvaluatedAt != nil {
		t.Errorf("PATCH spec did NOT clear last_evaluated_at (cache not invalidated): %v",
			*afterPatch.Goal.LastEvaluatedAt)
	}

	// Fresh read after PATCH must recompute → NEW timestamp AND now
	// fail (target 7200 vs current 4000).
	// Wait a millisecond so `now()` on the DB clock advances beyond
	// firstTs — otherwise the "new timestamp" assertion could false-
	// positive on a fast enough machine.
	time.Sleep(2 * time.Millisecond)
	rec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("progress GET 2: status %d body=%s", rec2.Code, rec2.Body.String())
	}
	var prog2 struct {
		Hit      bool    `json:"hit"`
		Progress float64 `json:"progress"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &prog2)
	if prog2.Hit {
		t.Errorf("progress 2 should miss under new higher target: got hit=true")
	}
	// Fetch the goal row again to confirm the timestamp moved forward.
	getRec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterSecond struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec2.Body.Bytes(), &afterSecond)
	if afterSecond.Goal.LastEvaluatedAt == nil || !afterSecond.Goal.LastEvaluatedAt.After(firstTs) {
		t.Errorf("second read did not produce a fresh timestamp: got %v, want > %v",
			afterSecond.Goal.LastEvaluatedAt, firstTs)
	}
}

// TestGoalsIngestInvalidation is the load-bearing test for the
// heartbeat-ingest → goal cache invalidation hook. Without the hook,
// the second progress read would still hit the 60s-old cache and
// return the same timestamp. WITH the hook, the ingest sets
// last_evaluated_at NULL and the next read recomputes.
func TestGoalsIngestInvalidation(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	owner, token := hz.MintUser("goal_ingest")

	// Preseed some Go time so the first read has something to sum.
	seedRollupForOwner(t, hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 1000)

	id := createGoal(t, e, token, "ingest-test", weeklyGoSpec)

	// First read → cache lands.
	rec1 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("progress 1: %d body=%s", rec1.Code, rec1.Body.String())
	}
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterFirst struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &afterFirst)
	if afterFirst.Goal.LastEvaluatedAt == nil {
		t.Fatalf("first progress read did not populate cache")
	}

	// Ingest a heartbeat. The invalidation hook nulls the cache row.
	now := float64(time.Now().Unix())
	ingestBody := []map[string]any{{
		"time":       now,
		"entity":     "a.go",
		"type":       "file",
		"project":    "P",
		"language":   "Go",
		"user_agent": "wakatime/1 (Linux) go/1 vscode",
	}}
	ingRec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/heartbeats.bulk", token, ingestBody)
	if ingRec.Code != http.StatusAccepted {
		t.Fatalf("heartbeat ingest: status %d body=%s", ingRec.Code, ingRec.Body.String())
	}

	// Read the goal row again — cache must be cleared.
	getRec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterIngest struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec2.Body.Bytes(), &afterIngest)
	if afterIngest.Goal.LastEvaluatedAt != nil {
		t.Errorf("heartbeat ingest did NOT invalidate goal cache (last_evaluated_at still %v). Without the invalidation hook wired in storeAndRespond, this test fails.",
			*afterIngest.Goal.LastEvaluatedAt)
	}
}

// TestGoalsBatchProgressEndpoint asserts the batched endpoint's
// contract: one HTTP call returns a map keyed by id, and disabled
// goals are omitted (so tile renderers see "no data" rather than an
// out-of-date value).
func TestGoalsBatchProgressEndpoint(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	owner, token := hz.MintUser("goal_batch")

	seedRollupForOwner(t, hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)

	id1 := createGoal(t, e, token, "g-enabled", weeklyGoSpec)
	id2 := createGoal(t, e, token, "g-disabled", weeklyGoSpec)
	// Disable id2 via toggle.
	tglRec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals/"+id2+"/toggle", token, map[string]any{
		"enabled": false,
	})
	if tglRec.Code != http.StatusOK {
		t.Fatalf("toggle: %d %s", tglRec.Code, tglRec.Body.String())
	}

	batchRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/progress", token, nil)
	if batchRec.Code != http.StatusOK {
		t.Fatalf("batch progress: %d body=%s", batchRec.Code, batchRec.Body.String())
	}
	var env struct {
		Progress map[string]struct {
			Hit bool `json:"hit"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(batchRec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode batch: %v body=%s", err, batchRec.Body.String())
	}
	if _, ok := env.Progress[id1]; !ok {
		t.Errorf("enabled goal missing from batch: %+v", env.Progress)
	}
	if _, ok := env.Progress[id2]; ok {
		t.Errorf("disabled goal appears in batch: %+v", env.Progress)
	}
}

// TestGoalsDuplicateNameReturns409 covers the collision path for
// CREATE. A duplicate must surface as 409, not 500 (leaked DB error).
func TestGoalsDuplicateNameReturns409(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_dup")

	r1 := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": "same", "spec": json.RawMessage(weeklyGoSpec),
	})
	if r1.Code != http.StatusOK {
		t.Fatalf("first POST: %d %s", r1.Code, r1.Body.String())
	}
	r2 := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": "same", "spec": json.RawMessage(weeklyGoSpec),
	})
	if r2.Code != http.StatusConflict {
		t.Errorf("duplicate name: status %d, want 409 (body=%s)", r2.Code, r2.Body.String())
	}
}

// TestGoalsProgressServesFromCacheWithinTTL is the LOAD-BEARING
// mirror of TestGoalsProgressCacheAndFreshness: two rapid reads inside
// the TTL must return the SAME cached bytes and NOT bump
// last_evaluated_at. The prior test only asserts the timestamp moves
// forward AFTER a PATCH — a regression that recomputed on EVERY read
// (cache never used) would still pass that test but fail this one.
//
// Anti-tautology anchor: we assert equality of the two timestamps
// (second read reused the first read's cache row). Bump the TTL to
// zero in stats/goals.go and this test fails (timestamps drift).
func TestGoalsProgressServesFromCacheWithinTTL(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	owner, token := hz.MintUser("goal_cachehit")

	seedRollupForOwner(t, hz, owner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)
	id := createGoal(t, e, token, "cachehit", weeklyGoSpec)

	// First read: compute + cache. Capture the timestamp.
	rec1 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("progress 1: %d body=%s", rec1.Code, rec1.Body.String())
	}
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterFirst struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &afterFirst)
	if afterFirst.Goal.LastEvaluatedAt == nil {
		t.Fatalf("first progress read did not populate cache")
	}
	firstTs := *afterFirst.Goal.LastEvaluatedAt

	// Second read INSIDE TTL (immediate) must reuse the cache row.
	// Sleep briefly so a bug that recomputes would produce a clearly
	// distinguishable timestamp (DB now() tick > 1ms).
	time.Sleep(5 * time.Millisecond)
	rec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id+"/progress", token, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("progress 2: %d body=%s", rec2.Code, rec2.Body.String())
	}

	// Assertion 1: body of the second read matches the first byte-for-byte
	// (cache row is JSONB and returned verbatim on serve-from-cache).
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("body diverged inside TTL:\n  first: %s\n second: %s",
			rec1.Body.String(), rec2.Body.String())
	}

	// Assertion 2: last_evaluated_at UNCHANGED (cache hit means no
	// UpdateGoalProgress ran, so the timestamp column is untouched).
	getRec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var afterSecond struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec2.Body.Bytes(), &afterSecond)
	if afterSecond.Goal.LastEvaluatedAt == nil {
		t.Fatalf("second read wiped the cache row (should have served from cache)")
	}
	if !afterSecond.Goal.LastEvaluatedAt.Equal(firstTs) {
		t.Errorf("last_evaluated_at changed on cache-hit read: was %v, now %v (cache path not taken?)",
			firstTs, *afterSecond.Goal.LastEvaluatedAt)
	}
}

// TestGoalsBatchProgressOwnerScoping proves the batched endpoint is
// owner-scoped. Alice and Bob each own a goal; alice's call returns
// only her id. A regression that dropped the sender filter on the
// underlying ListGoals would leak bob's id into alice's batch map.
func TestGoalsBatchProgressOwnerScoping(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	aliceOwner, aliceTok := hz.MintUser("goal_batch_a")
	bobOwner, bobTok := hz.MintUser("goal_batch_b")

	// Seed rollup for both — proves the eval branch runs for each.
	seedRollupForOwner(t, hz, aliceOwner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)
	seedRollupForOwner(t, hz, bobOwner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)

	aID := createGoal(t, e, aliceTok, "a-batch", weeklyGoSpec)
	bID := createGoal(t, e, bobTok, "b-batch", weeklyGoSpec)

	// Alice's batch: exactly {aID}, NOT bID.
	rec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/progress", aliceTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch alice: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Progress map[string]any `json:"progress"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if _, ok := env.Progress[bID]; ok {
		t.Errorf("alice's batch LEAKED bob's goal id %s — batch endpoint dropped owner filter", bID)
	}
	if _, ok := env.Progress[aID]; !ok {
		t.Errorf("alice's own goal missing from batch: %+v", env.Progress)
	}
	if len(env.Progress) != 1 {
		t.Errorf("alice batch len = %d, want 1", len(env.Progress))
	}
}

// TestGoalsIngestInvalidationOwnerScoping proves the ingest hook is
// scoped to the ingesting user — bob's heartbeat MUST NOT wipe
// alice's cache. Without this test, a bug that called
// InvalidateGoalsForOwner("*") or lost the owner param would pass
// TestGoalsIngestInvalidation (which only checks the ingester's own
// cache).
func TestGoalsIngestInvalidationOwnerScoping(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	aliceOwner, aliceTok := hz.MintUser("goal_ing_scope_a")
	_, bobTok := hz.MintUser("goal_ing_scope_b")

	// Alice seeds rollup + goal + reads progress → cache lands.
	seedRollupForOwner(t, hz, aliceOwner, time.Now().UTC().AddDate(0, 0, -1), "Go", 5000)
	aID := createGoal(t, e, aliceTok, "a-scoped", weeklyGoSpec)
	if r := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+aID+"/progress", aliceTok, nil); r.Code != http.StatusOK {
		t.Fatalf("alice progress: %d body=%s", r.Code, r.Body.String())
	}
	// Capture alice's cache timestamp.
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+aID, aliceTok, nil)
	var afterAliceRead struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &afterAliceRead)
	if afterAliceRead.Goal.LastEvaluatedAt == nil {
		t.Fatalf("alice cache didn't land")
	}
	aliceTsBefore := *afterAliceRead.Goal.LastEvaluatedAt

	// BOB ingests a heartbeat. Alice's cache must survive.
	now := float64(time.Now().Unix())
	ingestBody := []map[string]any{{
		"time":       now,
		"entity":     "b.py",
		"type":       "file",
		"project":    "P",
		"language":   "Python",
		"user_agent": "wakatime/1 (Linux) go/1 vscode",
	}}
	ingRec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/heartbeats.bulk", bobTok, ingestBody)
	if ingRec.Code != http.StatusAccepted {
		t.Fatalf("bob ingest: %d body=%s", ingRec.Code, ingRec.Body.String())
	}

	// Alice's cache row must still be there, timestamp UNCHANGED.
	getRec2 := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+aID, aliceTok, nil)
	var afterBobIngest struct {
		Goal struct {
			LastEvaluatedAt *time.Time `json:"lastEvaluatedAt"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec2.Body.Bytes(), &afterBobIngest)
	if afterBobIngest.Goal.LastEvaluatedAt == nil {
		t.Errorf("bob's ingest WIPED alice's cache — invalidation is not owner-scoped")
	} else if !afterBobIngest.Goal.LastEvaluatedAt.Equal(aliceTsBefore) {
		t.Errorf("alice's cache timestamp drifted after bob's ingest: was %v, now %v",
			aliceTsBefore, *afterBobIngest.Goal.LastEvaluatedAt)
	}
}

// TestGoalsToggleHTTP asserts the toggle endpoint actually flips at
// the HTTP layer. All other tests only use toggle to DISABLE a goal
// (TestGoalsBatchProgressEndpoint) or check owner-scoping (404); no
// test verifies the response contract {"enabled": bool} matches the
// eventual state. A regression that returned a stale value (say,
// always the old enabled) would pass owner-scoping + batch tests but
// fail this one.
func TestGoalsToggleHTTP(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_toggle_http")

	id := createGoal(t, e, token, "toggle-http", weeklyGoSpec)

	// Flip 1: no body, expect enabled=false.
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("flip 1: %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode flip 1: %v", err)
	}
	if env.Enabled {
		t.Errorf("flip 1: response enabled=true (want false — starting from default true)")
	}
	// GET confirms the DB state matches the response.
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var got struct {
		Goal struct {
			Enabled bool `json:"enabled"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &got)
	if got.Goal.Enabled {
		t.Errorf("GET after flip 1: enabled=true (response and DB desynced)")
	}

	// Flip 2: no body — back to true.
	rec2 := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token, nil)
	var env2 struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &env2)
	if !env2.Enabled {
		t.Errorf("flip 2: response enabled=false (want true)")
	}

	// Exact-set enabled=true on already-true — idempotent 200 with true.
	rec3 := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token, map[string]any{
		"enabled": true,
	})
	if rec3.Code != http.StatusOK {
		t.Errorf("idempotent set true=true: %d body=%s", rec3.Code, rec3.Body.String())
	}
	var env3 struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(rec3.Body.Bytes(), &env3)
	if !env3.Enabled {
		t.Errorf("idempotent set: response enabled=%v (want true)", env3.Enabled)
	}
}

// TestGoalsValidationRejectsCoversAllBranches complements the existing
// TestGoalsValidationRejects with the missing branches (streak errors,
// depth cap, empty any, not-arity, active_days windows). The value of
// listing every branch: someone loosening the validator to remove one
// specific check will get exactly ONE red test with a clear name.
func TestGoalsValidationRejectsCoversAllBranches(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_reject_full")

	// Build a depth-6 spec (over the depth-5 cap).
	deep := `{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"}`
	for i := 0; i < 6; i++ {
		deep = `{"kind":"all","of":[` + deep + `]}`
	}

	bad := map[string]string{
		"streak missing condition": `{"kind":"streak","min_days":3}`,
		"streak min_days negative": `{"kind":"streak","min_days":-1,"condition":{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"day"}}`,
		"streak min_days too big":  `{"kind":"streak","min_days":9999,"condition":{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"day"}}`,
		"empty any":                `{"kind":"any","of":[]}`,
		"not wrong arity 0":        `{"kind":"not","of":[]}`,
		"not wrong arity 2": `{"kind":"not","of":[
			{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},
			{"kind":"time","axis":"project","value":null,"op":">=","target_seconds":1,"window":"week"}
		]}`,
		"active_days negative n":       `{"kind":"active_days","op":">=","n":-1,"window":"week"}`,
		"active_days invalid window":   `{"kind":"active_days","op":">=","n":1,"window":"day"}`,
		"depth cap":                    deep,
		"nested bad axis inside all":   `{"kind":"all","of":[{"kind":"time","axis":"language","value":null,"op":">=","target_seconds":1,"window":"week"},{"kind":"time","axis":"chicken","value":null,"op":">=","target_seconds":1,"window":"week"}]}`,
	}
	for name, spec := range bad {
		t.Run(name, func(t *testing.T) {
			rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
				"name": "vr_" + name,
				"spec": json.RawMessage(spec),
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			// Body should carry a non-empty error hint so the FE can
			// display it. A blank message would mean the handler
			// silently rejected, which is user-hostile.
			body := rec.Body.String()
			if len(body) < 5 {
				t.Errorf("body too short (no error hint): %q", body)
			}
		})
	}
}

// TestGoalsCreateMissingFields checks the shape-level guards that
// live in the handler ABOVE ValidateSpec: empty name → 400, empty
// spec → 400. Regressions that fall through to CreateGoal would
// either 500 (DB error) or silently succeed with empty name — both
// user-hostile.
func TestGoalsCreateMissingFields(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_missing_fields")

	// Empty name.
	rec := doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": "",
		"spec": json.RawMessage(weeklyGoSpec),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name: status %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	// Whitespace-only name (strings.TrimSpace check).
	rec = doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": "   ",
		"spec": json.RawMessage(weeklyGoSpec),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("whitespace-only name: status %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	// Missing spec (nil rawmessage).
	rec = doJSONReq(t, e, http.MethodPost, "/api/v1/users/current/goals", token, map[string]any{
		"name": "no-spec",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing spec: status %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
}

// TestGoalsPatchValidationAndFields exercises the PATCH-side
// validator + name-cannot-be-empty guard. Regressions in either would
// pass tests that only touch the POST path.
func TestGoalsPatchValidationAndFields(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_patch_val")

	id := createGoal(t, e, token, "patch-val", weeklyGoSpec)

	// PATCH with invalid spec → 400.
	rec := doJSONReq(t, e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
		"spec": json.RawMessage(`{"kind":"time","axis":"CHICKEN","op":">=","target_seconds":1,"window":"week"}`),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH bad spec: status %d, want 400 body=%s", rec.Code, rec.Body.String())
	}

	// PATCH with whitespace-only name → 400.
	rec = doJSONReq(t, e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
		"name": "   ",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH empty name: status %d, want 400 body=%s", rec.Code, rec.Body.String())
	}

	// PATCH with a valid new name → 200, name persisted.
	rec = doJSONReq(t, e, http.MethodPatch, "/api/v1/users/current/goals/"+id, token, map[string]any{
		"name": "renamed",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH valid: %d body=%s", rec.Code, rec.Body.String())
	}
	getRec := doJSONReq(t, e, http.MethodGet, "/api/v1/users/current/goals/"+id, token, nil)
	var got struct {
		Goal struct {
			Name string `json:"name"`
		} `json:"goal"`
	}
	_ = json.Unmarshal(getRec.Body.Bytes(), &got)
	if got.Goal.Name != "renamed" {
		t.Errorf("PATCH name persisted as %q, want %q", got.Goal.Name, "renamed")
	}
}

// TestGoalsDuplicateNameOnRename409 covers a subtle path: PATCHing an
// existing goal to a name that another goal (same owner) already has
// must return 409, not 500. The DB layer surfaces isUniqueViolation
// on the RENAME UPDATE too; the handler must catch it via the same
// mapping.
func TestGoalsDuplicateNameOnRename409(t *testing.T) {
	hz := testutil.NewHarness(t)
	e := hz.Router()
	_, token := hz.MintUser("goal_rename_dup")

	_ = createGoal(t, e, token, "existing-name", weeklyGoSpec)
	id2 := createGoal(t, e, token, "second-name", weeklyGoSpec)

	// Rename id2 → "existing-name" → 409.
	rec := doJSONReq(t, e, http.MethodPatch, "/api/v1/users/current/goals/"+id2, token, map[string]any{
		"name": "existing-name",
	})
	if rec.Code != http.StatusConflict {
		t.Errorf("rename-to-existing: status %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
}

// (helpers) — doJSONReq is defined in password_test.go; semanticJSONDiff
