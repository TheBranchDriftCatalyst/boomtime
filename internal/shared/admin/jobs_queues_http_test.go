// jobs_queues_http_test.go — HTTP auth-gate coverage for GET
// /api/v1/admin/jobs/queues (boom-hney queue overview). Mirrors the metrics
// endpoint's gate test: unauth'd ⇒ 4xx, non-admin ⇒ 403 with no allowlist leak.
// The 200-with-data path is covered end-to-end by the store-level
// TestListJobKindStats (a wired JobStore needs a live pool); here we only assert
// the requireAdmin/CapAdmin gate fires before any jobs work.
package admin_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

func jobsRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	admin.Register(e, hz.H.Admin)
	return e
}

var _ = Describe("Admin jobs queues: auth gates", func() {
	It("rejects unauth'd (4xx) and non-admin (403) callers", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		e := jobsRouter(hz)

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/queues", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400), "unauth'd must be rejected")

		nonAdmin, nonAdminToken := hz.MintUser("jobs_queues_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-dave": {}}
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/queues", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).NotTo(ContainSubstring(nonAdmin))
		Expect(rec.Body.String()).NotTo(ContainSubstring("secret-admin-dave"))
	})
})
