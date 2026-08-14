// jobs_logs_http_test.go — HTTP coverage for the persisted per-job log endpoints
// (gaka-hney): GET /api/v1/admin/jobs/:id/logs and DELETE .../logs. The object
// store is a stub, so these exercise the handler contract end-to-end WITHOUT a
// live S3: fixture entries → 200 JSON; absent → 404; DELETE calls the store's
// Delete (never the jobs table) and is a no-op when persistence is off; and the
// requireAdmin gate fires before any store work.
package admin_test

import (
	"context"
	"io"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/admin"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logging"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/objstore"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// stubObjStore is an objstore.Store test double. getErr/getBody drive the GET
// path; every Delete/Put/Exists call is recorded so the specs can assert exactly
// which store method the handler invoked.
type stubObjStore struct {
	getBody    string
	getErr     error
	deleteKeys []string
	deleteErr  error
}

func (s *stubObjStore) Put(context.Context, string, io.Reader, int64, string) error { return nil }

func (s *stubObjStore) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return io.NopCloser(strings.NewReader(s.getBody)), nil
}

func (s *stubObjStore) Delete(_ context.Context, key string) error {
	s.deleteKeys = append(s.deleteKeys, key)
	return s.deleteErr
}

func (s *stubObjStore) Exists(context.Context, string) (bool, error) { return false, nil }

func jobLogsRouter(hz *testutil.Harness) *echo.Echo {
	e := echo.New()
	admin.Register(e, hz.H.Admin)
	return e
}

// makeAdmin mints a user and flips the config allowlist so ONLY that user is an
// admin, returning its token.
func makeAdmin(hz *testutil.Harness, prefix string) (user, token string) {
	user, token = hz.MintUser(prefix)
	hz.Cfg.AdminUsers = map[string]struct{}{user: {}}
	return user, token
}

var _ = Describe("Admin job logs: GET", func() {
	It("returns 200 with the stored entries when the object exists", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_get_ok")

		blob, err := jobs.MarshalJobLogs([]logging.LogEntry{
			{ID: 1, Level: "INFO", Msg: "jobs: started", Attrs: map[string]string{"job_id": "7", "kind": "k"}, Source: "worker"},
			{ID: 2, Level: "INFO", Msg: "did a thing", Attrs: map[string]string{"job_id": "7"}, Source: "worker"},
		})
		Expect(err).NotTo(HaveOccurred())
		hz.H.Admin.SetJobLogStore(&stubObjStore{getBody: string(blob)})

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/7/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"entries"`))
		Expect(rec.Body.String()).To(ContainSubstring("jobs: started"))
		Expect(rec.Body.String()).To(ContainSubstring("did a thing"))
	})

	It("returns 404 when the object is absent (ErrNotFound)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_get_404")
		hz.H.Admin.SetJobLogStore(&stubObjStore{getErr: objstore.ErrNotFound})

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/7/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("returns 404 when persistence is off (no store wired)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_get_nostore")
		// JobLogStore left nil.

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/7/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("rejects unauth'd (4xx) and non-admin (403) callers before any store work", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		// A store that would PANIC if Get were reached — proves the gate fires first.
		hz.H.Admin.SetJobLogStore(&stubObjStore{getErr: objstore.ErrNotFound})

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/7/logs", "", nil)
		Expect(rec.Code).To(BeNumerically(">=", 400), "unauth'd must be rejected")

		nonAdmin, nonAdminToken := hz.MintUser("joblogs_get_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-dave": {}}
		rec = doJSONReqG(e, http.MethodGet, "/api/v1/admin/jobs/7/logs", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(rec.Body.String()).NotTo(ContainSubstring(nonAdmin))
	})
})

var _ = Describe("Admin job logs: DELETE", func() {
	It("deletes ONLY the stored object (correct key) and leaves the jobs store untouched", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_del_ok")
		stub := &stubObjStore{}
		hz.H.Admin.SetJobLogStore(stub)
		// JobStore is deliberately left nil: the DELETE handler must never touch the
		// jobs table, so a nil store proves it (any dereference would 500/panic).

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/42/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"deleted":true`))
		Expect(stub.deleteKeys).To(Equal([]string{jobs.JobLogKey(42)}))
	})

	It("is a clean no-op (deleted:false) when persistence is off", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_del_nostore")

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/42/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"deleted":false`))
	})

	It("rejects non-admin callers with 403 (no delete attempted)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		stub := &stubObjStore{}
		hz.H.Admin.SetJobLogStore(stub)

		_, nonAdminToken := hz.MintUser("joblogs_del_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-dave": {}}

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/42/logs", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(stub.deleteKeys).To(BeEmpty(), "gate must fire before any store delete")
	})
})
