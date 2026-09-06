// heartbeat_time_validation_test.go — regression for the unvalidated
// heartbeat `time` field (2026-09-06 audit, heartbeats.go).
//
// Named invariant: "a heartbeat with a missing/zero `time` is rejected at the
// handler and never reaches the ingest transaction."
//
// Why it matters, and why the assertions are shaped the way they are:
// model.HeartbeatPayload.TimeSent is a bare float64, so an omitted "time" key
// binds to 0 and used to be stored as 1970-01-01. db.saveHeartbeats derives
// its phase-3 maintenance window from the batch MINIMUM timestamp, so that
// single row made refreshRollup DELETE the sender's whole hb_rollup_daily and
// re-aggregate every heartbeat they own — inside the ingest transaction — and
// in a mixed batch it stretched recomputeGapsUntil across all of history too.
// The 1970 row then permanently skewed clampStartToData and every "All time"
// chart.
//
// The rollup rebuild is invisible in the response (it recomputes the same
// numbers, just slowly), so the durable proof is the pair of "nothing older
// than 2000 exists for this owner" checks: under the pre-fix code the 1970
// heartbeat row AND its 1970-01-01 rollup row both materialize.
package ingest_test

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// countRowsBefore2000 returns (heartbeats, rollup rows) dated before the
// plausibility floor for one owner. Both must always be zero.
func countRowsBefore2000(hz *testutil.Harness, owner string) (int64, int64) {
	ctx := context.Background()
	var hb, rollup int64
	Expect(hz.DB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM heartbeats WHERE sender=$1 AND time_sent < '2000-01-01'`,
		owner).Scan(&hb)).To(Succeed())
	Expect(hz.DB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM hb_rollup_daily WHERE sender=$1 AND day < '2000-01-01'`,
		owner).Scan(&rollup)).To(Succeed())
	return hb, rollup
}

var _ = Describe("heartbeat `time` validation (2026-09-06 audit)", func() {
	It("rejects a single heartbeat with NO `time` key — 400, nothing persisted at the epoch", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, tok := hz.MintUser("hb_no_time")

		before := countHeartbeats(hz, owner)
		// Deliberately no "time" key: this is the exact wire shape that
		// binds TimeSent to the float64 zero value.
		body := map[string]any{
			"entity":     "main.go",
			"type":       "file",
			"project":    "P",
			"user_agent": "wakatime/1 (Linux) go/1 vscode",
		}
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/heartbeats", tok, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("time"),
			"the 400 must name the offending field so a plugin author can fix it; got %s", rec.Body.String())

		Expect(countHeartbeats(hz, owner)).To(Equal(before), "no row may be persisted")
		hb, rollup := countRowsBefore2000(hz, owner)
		Expect(hb).To(BeZero(), "a zero `time` must not land a 1970-01-01 heartbeat")
		Expect(rollup).To(BeZero(), "no 1970 rollup bucket may be created")
	})

	It("rejects the WHOLE bulk batch when one beat carries time:0 — the live beats are not partially applied", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, tok := hz.MintUser("hb_zero_time_bulk")

		// Seed real history + its rollup first: this is the state the
		// pre-fix full-history DELETE + re-aggregate would have churned.
		day := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
		sd := hz.Seeder(owner).Projects("P")
		sd.Block(testutil.HB{Project: "P", Language: "Go", Entity: "a.go", Category: "Coding"}, day, 5, 60)
		sd.RefreshRollup(day.Add(-24 * time.Hour))

		before := countHeartbeats(hz, owner)
		now := float64(time.Now().Unix())
		body := []map[string]any{
			{"time": now, "entity": "a.go", "type": "file", "project": "P", "user_agent": "wakatime/1 (Linux) go/1 vim"},
			{"time": 0, "entity": "b.go", "type": "file", "project": "P", "user_agent": "wakatime/1 (Linux) go/1 vim"},
			{"time": now + 1, "entity": "c.go", "type": "file", "project": "P", "user_agent": "wakatime/1 (Linux) go/1 vim"},
		}
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/heartbeats.bulk", tok, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("heartbeat[1]"),
			"the 400 must identify WHICH element is bad; got %s", rec.Body.String())

		// All-or-nothing: the response envelope is positional, so dropping
		// just the bad element would misalign every id the client correlates.
		Expect(countHeartbeats(hz, owner)).To(Equal(before),
			"a rejected batch must persist none of its elements")
		hb, rollup := countRowsBefore2000(hz, owner)
		Expect(hb).To(BeZero(), "the epoch beat must never reach the heartbeats table")
		Expect(rollup).To(BeZero(),
			"no 1970 rollup bucket — the full-history refreshRollup(since=1970) must never be triggered")
	})

	It("still accepts an ordinary current-time batch (the guard must not break normal ingest)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		owner, tok := hz.MintUser("hb_valid_time")

		before := countHeartbeats(hz, owner)
		now := float64(time.Now().Unix())
		body := []map[string]any{
			{"time": now, "entity": "a.go", "type": "file", "project": "P", "user_agent": "wakatime/1 (Linux) go/1 vim"},
			{"time": now - 3600, "entity": "b.go", "type": "file", "project": "P", "user_agent": "wakatime/1 (Linux) go/1 vim"},
		}
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/heartbeats.bulk", tok, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusAccepted), "body=%s", rec.Body.String())
		Expect(countHeartbeats(hz, owner)).To(Equal(before + 2))
	})
})
