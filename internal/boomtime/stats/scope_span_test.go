// scope_span_test.go — regression for the unbounded dashboard day series
// (2026-09-06 audit, "gap-fill helpers emit unbounded per-day arrays").
//
// Named invariant: "an absurd ?start/?end range cannot make a dashboard
// response allocate one entry per day of recorded history."
//
// ?start and ?end are unvalidated client input, parsed by apihelpers with a
// bare "2006-01-02" layout. Sessions is the sharpest instance: unlike
// /stats and /projects/:project it has no clampStartToData, so
// ToSessionsPayload gap-fills a SessionDaily for literally every day between
// the two params. `?start=0001-01-01&end=9999-12-31` asked for ~3.65 million
// of them — an ~88 MB []time.Time, a same-order struct slice, a JSON body to
// match, and the whole thing then parked in the response cache. One
// authenticated GET per pod.
//
// dashboardScope now clamps the SPAN (keeping `end`, so the surviving window
// is the recent one where real data lives). The assertions below pin the
// exact clamped length AND that a legitimately long range is untouched — a
// clamp that quietly ate a normal "All time" query would be its own bug.
package stats_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// sessionsDailyLen issues a Sessions request and returns len(daily).
func sessionsDailyLen(e http.Handler, token, query string) int {
	rec := getJSONG(e, "/api/v1/users/current/stats/sessions"+query, token)
	ExpectWithOffset(1, rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
	var payload struct {
		Daily []map[string]any `json:"daily"`
	}
	ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &payload)).To(Succeed())
	return len(payload.Daily)
}

var _ = Describe("dashboard range span clamp (2026-09-06 audit)", func() {
	It("bounds the per-day series for an absurd range instead of emitting one entry per day since year 1", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := bigbetsRouter(hz)
		_, tok := hz.MintUser("span_clamp")

		// maxDashboardSpan is 25*365 days. end parses to midnight UTC, start
		// is clamped to exactly 9125 days earlier, and genDates is inclusive
		// of both endpoints => 9126 entries. Unclamped this is 3,652,060.
		got := sessionsDailyLen(e, tok, "?start=0001-01-01&end=9999-12-31")
		Expect(got).To(Equal(9126),
			"absurd range must collapse to the maxDashboardSpan window (25*365 days + 1); "+
				"got %d — a number in the millions means the span clamp is gone", got)
	})

	It("leaves a legitimately long (10-year) range alone", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := bigbetsRouter(hz)
		_, tok := hz.MintUser("span_noclamp")

		// 2016-01-01 .. 2026-01-01 = 3653 days (three leap days), +1 for the
		// inclusive end. Well inside the cap, so the clamp must not fire.
		got := sessionsDailyLen(e, tok, "?start=2016-01-01&end=2026-01-01")
		Expect(got).To(Equal(3654),
			"a 10-year 'All time' window is legitimate and must be returned whole; got %d", got)
	})
})
