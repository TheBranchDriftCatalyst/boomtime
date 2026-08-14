// metrics_http_test.go — HTTP coverage for GET /api/v1/admin/metrics
// (gaka-metrics). Named invariants:
//
//   - admin gate: unauth'd ⇒ 4xx, non-admin ⇒ 403 (no allowlist leak);
//   - admin ⇒ 200 with a {series:[...]} envelope, and a series that was
//     Inc'd on the process-global registry shows up with its points;
//   - ?names= prefix filter narrows the returned series.
package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

func metricsRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	admin.Register(e, hz.H.Admin)
	return e
}

type metricsEnvelope struct {
	Series []struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Points []struct {
			Bucket time.Time `json:"bucket"`
			Value  float64   `json:"value"`
		} `json:"points"`
	} `json:"series"`
}

var _ = Describe("Admin metrics: auth gates", func() {
	It("rejects unauth'd (4xx) and non-admin (403) callers", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := metricsRouter(hz)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/metrics", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400), "unauth'd must be rejected")

		nonAdmin, nonAdminToken := hz.MintUser("metrics_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-dave": {}}
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/admin/metrics", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).NotTo(ContainSubstring(nonAdmin))
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret-admin-dave"))
	})
})

var _ = Describe("Admin metrics: GET /api/v1/admin/metrics", func() {
	It("returns the registry snapshot with an instrumented series", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := metricsRouter(hz)
		user, token := hz.MintUser("metrics_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// A uniquely-named series so the assertion is robust against whatever
		// other series the process has accumulated.
		name := fmt.Sprintf("test.http.series.%d", time.Now().UnixNano())
		metrics.Inc(name, 3)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/metrics", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var env metricsEnvelope
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())

		var found bool
		for _, s := range env.Series {
			if s.Name == name {
				found = true
				Expect(s.Kind).To(Equal("counter"))
				var sum float64
				for _, p := range s.Points {
					sum += p.Value
				}
				Expect(sum).To(Equal(float64(3)))
			}
		}
		Expect(found).To(BeTrue(), "Inc'd series %q must appear in the snapshot", name)
	})

	It("filters series by the ?names= prefix", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := metricsRouter(hz)
		user, token := hz.MintUser("metrics_filter_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		prefix := fmt.Sprintf("zz_filter_%d.", time.Now().UnixNano())
		metrics.Inc(prefix+"kept", 1)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/metrics?names="+prefix, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var env metricsEnvelope
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		Expect(env.Series).NotTo(BeEmpty())
		for _, s := range env.Series {
			Expect(s.Name).To(HavePrefix(prefix), "prefix filter must exclude other series")
		}
	})
})
