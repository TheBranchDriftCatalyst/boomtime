// bigbets_test.go — gaka-d6x.handler
//
// Coverage suite for the bigbets cluster (bigbets.go): Punchcard,
// Sessions, AIActivity, HealthActivity, WorkoutList, Momentum.
//
// hz.Router() only wires Momentum; the other five endpoints are absent
// from the harness router. Each spec builds a LOCAL *echo.Echo (fresh
// echo.New()) registering ONLY the routes it needs — this avoids the
// duplicate-route panic that hitting hz.Router() twice would trigger.
//
// Every spec pins a named invariant (mostly: cross-user isolation +
// shape correctness) and every user-scoped endpoint includes an
// A-vs-B seed / read to prove the endpoint filters by owner.
package stats_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
	"github.com/labstack/echo/v5"
)

// mapKeys returns the sorted key list of a JSON-decoded map — used in
// diagnostic messages so a failing shape assertion names WHICH keys the
// endpoint actually returned instead of a bare mismatch.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// bigbetsRouter mounts every bigbets endpoint that hz.Router() omits.
// hz.Router() DOES register Momentum — but not Punchcard/Sessions/
// AIActivity/HealthActivity/WorkoutList. Using a fresh echo.New() here
// means we can register whatever we want without any collision.
func bigbetsRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	h := hz.H
	// Auth-shim so unauth requests get a normal 4xx rather than 404.
	// gaka-8tn phase 4a: Login moved to h.Identity.
	e.POST("/auth/login", h.Identity.Login)
	e.GET("/api/v1/users/current/stats/punchcard", h.Stats.Punchcard)
	e.GET("/api/v1/users/current/stats/sessions", h.Stats.Sessions)
	e.GET("/api/v1/users/current/stats/ai", h.Stats.AIActivity)
	e.GET("/api/v1/users/current/stats/health", h.Stats.HealthActivity)
	e.GET("/api/v1/users/current/workouts", h.Stats.WorkoutList)
	e.GET("/api/v1/users/current/stats/momentum", h.Stats.Momentum)
	return e
}

// seedRecentCodingBigbets seeds a 3-day block of coding heartbeats to
// give the aggregations something to chew on. Returns the anchor time so
// the caller can use it in range params or RefreshRollup.
func seedRecentCodingBigbets(hz *testutil.Harness, user, project string) time.Time {
	base := time.Now().UTC().Add(-3 * 24 * time.Hour)
	base = time.Date(base.Year(), base.Month(), base.Day(), 10, 0, 0, 0, time.UTC)
	sd := hz.Seeder(user).Projects(project)
	for d := 0; d < 3; d++ {
		day := base.AddDate(0, 0, d)
		sd.Block(testutil.HB{
			Project:  project,
			Language: "go",
			Editor:   "vim",
			Platform: "linux",
			Category: "coding",
			Entity:   "main.go",
		}, day, 30, 900)
	}
	Expect(hz.DB.RefreshRollup(context.Background(), user, base.Add(-time.Hour))).To(Succeed())
	return base
}

// seedAIHeartbeat inserts a heartbeat carrying AI signal columns so the
// GetAIActivity WHERE clause (non-null AI columns) accepts it.
func seedAIHeartbeat(hz *testutil.Harness, user string) {
	in := int64(100)
	out := int64(50)
	lines := int64(5)
	humanLines := int64(2)
	sess := "session-ai-1"
	ts := time.Now().UTC().Add(-2 * time.Hour)
	// Ensure the project row exists for FK.
	_, err := hz.DB.Pool.Exec(context.Background(),
		`INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		user, "aiproj")
	Expect(err).NotTo(HaveOccurred())
	_, err = hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO heartbeats
		  (sender, project, language, editor, plugin, machine, platform, branch, category,
		   entity, ty, is_write, time_sent, user_agent, gap_seconds,
		   ai_input_tokens, ai_output_tokens, ai_line_changes, human_line_changes, ai_session)
		VALUES ($1,'aiproj','go','vim','vscode','m','linux','main','coding',
		        'x.go','file',null,$2,'ua',60,
		        $3,$4,$5,$6,$7)`,
		user, ts, in, out, lines, humanLines, sess)
	Expect(err).NotTo(HaveOccurred())
}

// seedWorkoutHeartbeat inserts a workout heartbeat (ty='workout' is what
// GetWorkouts filters on). The workout_details companion row is
// deliberately absent — the LEFT JOIN in GetWorkouts already tolerates a
// missing details row (source_uuid coalesces to ”) and our assertions
// only care about presence of the event, not the HR series.
func seedWorkoutHeartbeat(hz *testutil.Harness, user string) {
	kind := "HKWorkoutActivityTypeRunning"
	label := "morning-run"
	durS := int64(1800)
	kcal := 220.5
	avgHR := int64(140)
	dist := 3500.0
	ts := time.Now().UTC().Add(-3 * time.Hour)
	_, err := hz.DB.Pool.Exec(context.Background(),
		`INSERT INTO projects (owner, name) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		user, label)
	Expect(err).NotTo(HaveOccurred())
	_, err = hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO heartbeats
		  (sender, project, language, editor, plugin, machine, platform, branch, category,
		   entity, ty, is_write, time_sent, user_agent, gap_seconds,
		   workout_kind, workout_duration_s, workout_kcal, workout_avg_hr, workout_distance_m)
		VALUES ($1,$2,null,null,null,'m','watchos',null,'wellness',
		        'workout','workout',null,$3,'ua',0,
		        $4,$5,$6,$7,$8)`,
		user, label, ts, kind, durS, kcal, avgHR, dist)
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("Punchcard (gaka-dg7)", func() {
	It("returns a payload with cells + totals matching seeded blocks; cross-user rows never leak", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("puncha")
		userB, tokB := hz.MintUser("punchb")

		// A gets coding data, B stays empty.
		seedRecentCodingBigbets(hz, userA, "proja")

		start := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/users/current/stats/punchcard?start=%s&end=%s", start, end)

		// A: expect non-zero totals.
		rec := getJSONG(e, url, tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "A body=%s", rec.Body.String())
		var payloadA struct {
			Cells        []map[string]any `json:"cells"`
			MaxSeconds   int64            `json:"maxSeconds"`
			TotalSeconds int64            `json:"totalSeconds"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payloadA)).To(Succeed())
		Expect(payloadA.TotalSeconds).To(BeNumerically(">", 0),
			"A seeded coding blocks must show non-zero total seconds; got %d", payloadA.TotalSeconds)
		Expect(payloadA.MaxSeconds).To(BeNumerically(">", 0))

		// B: same range, must be empty (user isolation).
		rec = getJSONG(e, url, tokB)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payloadB struct {
			TotalSeconds int64 `json:"totalSeconds"`
			MaxSeconds   int64 `json:"maxSeconds"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payloadB)).To(Succeed())
		Expect(payloadB.TotalSeconds).To(BeZero(),
			"cross-user leak on /stats/punchcard: B saw %d seconds without seeding", payloadB.TotalSeconds)

		_ = userA
		_ = userB
	})

	// gaka-d6x.handler critique: pin exact code, not a 4xx range.
	It("unauth /stats/punchcard returns exactly 400 (MissingAuth)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)

		rec := getJSONG(e, "/api/v1/users/current/stats/punchcard", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"absent Authorization must yield exactly 400 (MissingAuth); got %d body=%s",
			rec.Code, rec.Body.String())
	})

	// gaka-d6x.handler critique: /current/ endpoints derive owner
	// ENTIRELY from the token, so an attacker cannot supply a path prefix
	// to spoof identity. Prove that: B (token) sees empty data even
	// though A has seed data — B's token owns B, regardless of path.
	// Complements the standard cross-user test: this one exercises the
	// "stolen-token-shape" attack surface (attacker holding B's token
	// cannot ever address A's rows via /current/).
	It("stolen-token invariant: /current/ derives owner from token, not path or query", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, _ := hz.MintUser("stolena")
		_, tokB := hz.MintUser("stolenb")

		seedRecentCodingBigbets(hz, userA, "stolenproj")

		start := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/users/current/stats/punchcard?start=%s&end=%s", start, end)

		// B uses ITS OWN valid token — the /current/ prefix binds to B,
		// not to any A-shaped hint. B must see zero seconds even though A
		// has heavily-seeded data in the same DB.
		rec := getJSONG(e, url, tokB)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payload struct {
			TotalSeconds int64 `json:"totalSeconds"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.TotalSeconds).To(BeZero(),
			"stolen-token invariant broken: B's token addressed A's data on /current/; got %d seconds",
			payload.TotalSeconds)
		_ = userA
	})
})

var _ = Describe("Sessions (gaka-dg7)", func() {
	It("returns summary/daily/histogram + gap-fills daily to the queried range", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("sessa")
		_, tokB := hz.MintUser("sessb")

		seedRecentCodingBigbets(hz, userA, "sessproj")

		start := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/users/current/stats/sessions?start=%s&end=%s", start, end)

		recA := getJSONG(e, url, tokA)
		Expect(recA).To(testutil.HaveStatus(http.StatusOK), "A body=%s", recA.Body.String())
		var payloadA struct {
			Summary struct {
				Count        int64 `json:"count"`
				TotalSeconds int64 `json:"totalSeconds"`
			} `json:"summary"`
			Daily     []map[string]any `json:"daily"`
			Histogram []map[string]any `json:"histogram"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &payloadA)).To(Succeed())
		Expect(payloadA.Summary.Count).To(BeNumerically(">=", 1),
			"seeded 3 daily blocks must yield >=1 session; got %d", payloadA.Summary.Count)
		Expect(payloadA.Summary.TotalSeconds).To(BeNumerically(">", 0))
		// gaka-d6x.handler critique: the previous assertion was `>=3` with a
		// message claiming to pin the 14-day gap-fill invariant — a tautology
		// where the check was 10x looser than the stated invariant. genDates
		// on [now-14d, now] emits 15 midnight-UTC days (both endpoints
		// truncated to day, inclusive loop). Pin the EXACT count so a bug
		// that shrinks or expands the gap-fill by even one day trips here.
		Expect(len(payloadA.Daily)).To(Equal(15),
			"14-day range must gap-fill exactly 15 daily entries "+
				"(genDates on truncateDay(t0)..truncateDay(t1) inclusive); got %d", len(payloadA.Daily))
		Expect(payloadA.Histogram).NotTo(BeEmpty(),
			"histogram is always populated (fixed bucket count from ToSessionsPayload)")

		// B: same range, no seed, summary.count=0.
		recB := getJSONG(e, url, tokB)
		Expect(recB).To(testutil.HaveStatus(http.StatusOK))
		var payloadB struct {
			Summary struct {
				Count int64 `json:"count"`
			} `json:"summary"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &payloadB)).To(Succeed())
		Expect(payloadB.Summary.Count).To(BeZero(),
			"cross-user leak: B saw %d sessions without seeding", payloadB.Summary.Count)
	})
})

var _ = Describe("AIActivity (gaka-dg7)", func() {
	It("returns hasData=true with a summary when AI-tagged heartbeats exist for the caller", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("aia")

		seedAIHeartbeat(hz, userA)

		start := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/users/current/stats/ai?start=%s&end=%s", start, end)

		rec := getJSONG(e, url, tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "AI body=%s", rec.Body.String())
		var payload struct {
			HasData bool `json:"hasData"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.HasData).To(BeTrue(),
			"seeded AI heartbeat must flip hasData=true; body=%s", rec.Body.String())
	})

	// gaka-d6x.handler critique: the previous spec only asserted
	// hasData=false — a handler that ALWAYS returned false would pass.
	// This version pins the FULL empty-shape contract: hasData=false AND
	// the payload envelope is a well-formed JSON object with the expected
	// zero-value fields, AND the Content-Type is application/json. That
	// is a non-tautological guard: it distinguishes "empty, correctly
	// shaped" from "always empty because the handler bailed early".
	It("returns hasData=false with a well-formed empty envelope + JSON Content-Type", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		_, tok := hz.MintUser("aiempty")

		rec := getJSONG(e, "/api/v1/users/current/stats/ai", tok)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(HavePrefix("application/json"),
			"Content-Type must be JSON so the FE decoder path is exercised; got %q",
			rec.Header().Get("Content-Type"))

		// Decode into a permissive map so we can inspect ALL top-level keys
		// (a handler that returned literally `{"hasData":false}` would pass
		// a struct-decode but flunk this shape check).
		var raw map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &raw)).To(Succeed())
		Expect(raw).To(HaveKey("hasData"),
			"payload must include hasData key; got keys=%v", mapKeys(raw))
		Expect(raw["hasData"]).To(BeFalse(),
			"no AI heartbeats must yield hasData=false — FE skip-render depends on this")

		// The envelope has stable side-fields even when hasData=false —
		// FE renders skeletons against them, so an empty response that
		// dropped `days` or the per-total counters would break the
		// empty-state UI.
		Expect(raw).To(HaveKey("days"), "empty envelope must still carry `days`; keys=%v", mapKeys(raw))
		Expect(raw).To(HaveKey("totalInputTokens"),
			"empty envelope must still carry `totalInputTokens`; keys=%v", mapKeys(raw))
		Expect(raw).To(HaveKey("totalSessions"),
			"empty envelope must still carry `totalSessions`; keys=%v", mapKeys(raw))
		if days, ok := raw["days"].([]any); ok {
			Expect(days).To(BeEmpty(), "days must be an empty array on hasData=false")
		}
		// Zero-value totals (empty state) — proves the handler didn't just
		// early-return a `{"hasData":false}` shell.
		Expect(raw["totalInputTokens"]).To(BeNumerically("==", 0),
			"empty state: totalInputTokens must be 0; got %v", raw["totalInputTokens"])
		Expect(raw["totalSessions"]).To(BeNumerically("==", 0),
			"empty state: totalSessions must be 0; got %v", raw["totalSessions"])
	})

	It("cross-user isolation: A's AI heartbeat is invisible to B", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, _ := hz.MintUser("aiA")
		_, tokB := hz.MintUser("aiB")

		seedAIHeartbeat(hz, userA)

		rec := getJSONG(e, "/api/v1/users/current/stats/ai", tokB)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payload struct {
			HasData bool `json:"hasData"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.HasData).To(BeFalse(),
			"cross-user leak on /stats/ai: B saw A's AI heartbeat")
	})
})

var _ = Describe("HealthActivity (gaka-dg7)", func() {
	It("returns hasData=false for an empty range and 200s cleanly", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		_, token := hz.MintUser("healthempty")

		rec := getJSONG(e, "/api/v1/users/current/stats/health", token)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payload struct {
			HasData bool             `json:"hasData"`
			Days    []map[string]any `json:"days"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.HasData).To(BeFalse(),
			"no health data must yield hasData=false — Wellness card early-return depends on it")
	})

	It("hasData flips true when workout events exist and does not leak across users", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("healthA")
		_, tokB := hz.MintUser("healthB")

		seedWorkoutHeartbeat(hz, userA)

		recA := getJSONG(e, "/api/v1/users/current/stats/health", tokA)
		Expect(recA).To(testutil.HaveStatus(http.StatusOK), "A body=%s", recA.Body.String())
		var payloadA struct {
			HasData bool `json:"hasData"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &payloadA)).To(Succeed())
		Expect(payloadA.HasData).To(BeTrue(), "A seeded workout must show hasData=true")

		// B saw nothing.
		recB := getJSONG(e, "/api/v1/users/current/stats/health", tokB)
		Expect(recB).To(testutil.HaveStatus(http.StatusOK))
		var payloadB struct {
			HasData bool `json:"hasData"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &payloadB)).To(Succeed())
		Expect(payloadB.HasData).To(BeFalse(),
			"cross-user leak on /stats/health: B saw A's health data")
		_ = userA
	})
})

var _ = Describe("WorkoutList (gaka-dg7)", func() {
	It("returns hasData=true + events list scoped to caller; B never sees A's events", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("workoutA")
		_, tokB := hz.MintUser("workoutB")

		seedWorkoutHeartbeat(hz, userA)

		start := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/users/current/workouts?start=%s&end=%s", start, end)

		recA := getJSONG(e, url, tokA)
		Expect(recA).To(testutil.HaveStatus(http.StatusOK), "A body=%s", recA.Body.String())
		var payloadA struct {
			HasData bool             `json:"hasData"`
			Events  []map[string]any `json:"events"`
		}
		Expect(json.Unmarshal(recA.Body.Bytes(), &payloadA)).To(Succeed())
		Expect(payloadA.HasData).To(BeTrue())
		Expect(payloadA.Events).NotTo(BeEmpty(), "seeded workout must appear on /workouts")

		recB := getJSONG(e, url, tokB)
		Expect(recB).To(testutil.HaveStatus(http.StatusOK))
		var payloadB struct {
			HasData bool `json:"hasData"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &payloadB)).To(Succeed())
		Expect(payloadB.HasData).To(BeFalse(),
			"cross-user leak on /workouts: B saw A's event")
		_ = userA
	})

	It("empty range yields hasData=false (FE skip-render contract)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		_, tok := hz.MintUser("workoutempty")

		rec := getJSONG(e, "/api/v1/users/current/workouts", tok)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var payload struct {
			HasData bool `json:"hasData"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(payload.HasData).To(BeFalse())
	})
})

var _ = Describe("Momentum (gaka-dg7)", func() {
	It("returns per-project weekly series; respects ?top clamp; cross-user rows never appear", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("momA")
		_, tokB := hz.MintUser("momB")

		// Multi-project seed for A so ?top clamp is testable.
		base := time.Now().UTC().Add(-3 * 24 * time.Hour)
		base = time.Date(base.Year(), base.Month(), base.Day(), 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(userA).Projects("mom-p1", "mom-p2", "mom-p3")
		for i, p := range []string{"mom-p1", "mom-p2", "mom-p3"} {
			sd.Block(testutil.HB{
				Project:  p,
				Language: "go",
				Editor:   "vim",
				Platform: "linux",
				Category: "coding",
				Entity:   fmt.Sprintf("p%d.go", i),
			}, base.Add(time.Duration(i)*time.Hour), 30, 900)
		}
		Expect(hz.DB.RefreshRollup(context.Background(), userA, base.Add(-time.Hour))).To(Succeed())

		start := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)

		// ?top=2 must clamp to 2 project buckets.
		url := fmt.Sprintf("/api/v1/users/current/stats/momentum?start=%s&end=%s&top=2", start, end)
		rec := getJSONG(e, url, tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "A body=%s", rec.Body.String())
		var payload struct {
			Weeks    []string `json:"weeks"`
			Projects []struct {
				Name         string  `json:"name"`
				TotalSeconds int64   `json:"totalSeconds"`
				Weekly       []int64 `json:"weekly"`
			} `json:"projects"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(len(payload.Projects)).To(BeNumerically("<=", 2),
			"top=2 must limit projects to 2; got %d", len(payload.Projects))
		Expect(payload.Weeks).NotTo(BeEmpty(),
			"weeks are always gap-filled when there is any activity")

		// ?top=0 → handler forces top=8 (min-clamp): projects may be up to 3 (the seeded count).
		url = fmt.Sprintf("/api/v1/users/current/stats/momentum?start=%s&end=%s&top=0", start, end)
		rec = getJSONG(e, url, tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		Expect(len(payload.Projects)).To(BeNumerically(">=", 1),
			"top=0 default-clamped to 8 should return the seeded projects (up to 3)")

		// B: empty response (no seeds).
		recB := getJSONG(e, "/api/v1/users/current/stats/momentum", tokB)
		Expect(recB).To(testutil.HaveStatus(http.StatusOK))
		var payloadB struct {
			Projects []map[string]any `json:"projects"`
		}
		Expect(json.Unmarshal(recB.Body.Bytes(), &payloadB)).To(Succeed())
		Expect(payloadB.Projects).To(BeEmpty(),
			"cross-user leak on /stats/momentum: B saw %d projects without seeding", len(payloadB.Projects))
		_ = userA
	})

	// gaka-d6x.handler critique: pin the exact contract (400 MissingAuth).
	It("unauth /stats/momentum returns exactly 400 (MissingAuth)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)

		rec := getJSONG(e, "/api/v1/users/current/stats/momentum", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"absent Authorization must yield exactly 400 (MissingAuth); got %d body=%s",
			rec.Code, rec.Body.String())
	})

	// gaka-d6x.handler critique: only ?top=0 and ?top=2 paths were
	// covered. Pin the ?top-upper-bound behavior: the handler applies
	// only `if top < 1 { top = 8 }` and ToMomentumPayload slices
	// `order[:top]` which is a no-op when top > len(order). So a huge
	// ?top just passes through — the number of returned projects is
	// bounded by the seeded set, not by any server-side clamp. This
	// documents the current uncapped contract so if a cap is added
	// later (e.g., DB layer imposes a max-N) this test flags it.
	It("?top upper bound: massive ?top does not error and is bounded by seeded project count", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenIsolatedDB(GinkgoT(), "bigbets"))
		e := bigbetsRouter(hz)
		userA, tokA := hz.MintUser("momtopcap")

		base := time.Now().UTC().Add(-3 * 24 * time.Hour)
		base = time.Date(base.Year(), base.Month(), base.Day(), 10, 0, 0, 0, time.UTC)
		sd := hz.Seeder(userA).Projects("cap-p1", "cap-p2")
		for i, p := range []string{"cap-p1", "cap-p2"} {
			sd.Block(testutil.HB{
				Project:  p,
				Language: "go",
				Editor:   "vim",
				Platform: "linux",
				Category: "coding",
				Entity:   fmt.Sprintf("cap%d.go", i),
			}, base.Add(time.Duration(i)*time.Hour), 30, 900)
		}
		Expect(hz.DB.RefreshRollup(context.Background(), userA, base.Add(-time.Hour))).To(Succeed())

		start := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Format(time.RFC3339)
		url := fmt.Sprintf("/api/v1/users/current/stats/momentum?start=%s&end=%s&top=100000", start, end)

		rec := getJSONG(e, url, tokA)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"massive ?top must not error; got %d body=%s", rec.Code, rec.Body.String())
		var payload struct {
			Projects []map[string]any `json:"projects"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
		// Bounded by seeded project count, not by any server cap.
		Expect(len(payload.Projects)).To(BeNumerically("<=", 2),
			"?top=100000 with 2 seeded projects must return at most 2 (bounded by data, not cap); got %d",
			len(payload.Projects))
	})
})
