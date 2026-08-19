// metrics_http_test.go — HTTP coverage for GET /api/v1/admin/metrics
// (gaka-metrics). Named invariants:
//
//   - admin gate: unauth'd ⇒ 4xx, non-admin ⇒ 403 (no allowlist leak);
//   - admin ⇒ 200 with a {families:[...]} envelope Gathered from the
//     process-global Prometheus registry, and a counter registered on that
//     registry shows up with its value;
//   - ?names= prefix filter narrows the returned families.
package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func metricsRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	admin.Register(e, hz.H.Admin)
	return e
}

type metricsEnvelope struct {
	Families []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Samples []struct {
			Labels map[string]string `json:"labels"`
			Value  *float64          `json:"value"`
			Count  *uint64           `json:"count"`
			Sum    *float64          `json:"sum"`
		} `json:"samples"`
	} `json:"families"`
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
	It("returns the Gathered registry with an instrumented counter", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := metricsRouter(hz)
		user, token := hz.MintUser("metrics_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		// A uniquely-named counter registered on the SAME registry the endpoint
		// Gathers — robust against whatever else the process has accumulated.
		name := fmt.Sprintf("test_http_series_%d", time.Now().UnixNano())
		ctr := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: "test"})
		Expect(metrics.Registry.Register(ctr)).To(Succeed())
		ctr.Add(3)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/metrics", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var env metricsEnvelope
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())

		var found bool
		for _, f := range env.Families {
			if f.Name == name {
				found = true
				Expect(f.Type).To(Equal("counter"))
				Expect(f.Samples).To(HaveLen(1))
				Expect(f.Samples[0].Value).NotTo(BeNil())
				Expect(*f.Samples[0].Value).To(Equal(float64(3)))
			}
		}
		Expect(found).To(BeTrue(), "registered counter %q must appear in the gathered view", name)
	})

	It("filters families by the ?names= prefix", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := metricsRouter(hz)
		user, token := hz.MintUser("metrics_filter_admin")
		hz.Cfg.AdminUsers = map[string]struct{}{user: {}}

		prefix := fmt.Sprintf("zz_filter_%d_", time.Now().UnixNano())
		ctr := prometheus.NewCounter(prometheus.CounterOpts{Name: prefix + "kept", Help: "test"})
		Expect(metrics.Registry.Register(ctr)).To(Succeed())
		ctr.Inc()

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/metrics?names="+prefix, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var env metricsEnvelope
		Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
		Expect(env.Families).NotTo(BeEmpty())
		for _, f := range env.Families {
			Expect(f.Name).To(HavePrefix(prefix), "prefix filter must exclude other families")
		}
	})
})
