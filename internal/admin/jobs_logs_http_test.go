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
	"time"

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
	listKeys   []string // returned by List (the bulk-clear enumeration)
	listErr    error
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

func (s *stubObjStore) List(_ context.Context, _ string) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listKeys, nil
}

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

// DELETE /api/v1/admin/jobs/logs[?kind=] — the bulk log-clear (gaka-hney). It
// enumerates the stored log objects (objstore List) and deletes them; a ?kind=
// filter reads the jobs table for that kind's ids to keep only matching keys.
// Object storage only — jobs-table rows are never mutated.
var _ = Describe("Admin job logs: bulk clear (DELETE /jobs/logs)", func() {
	It("clears ALL stored logs (every listed key) when no kind is given", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_clear_all")
		stub := &stubObjStore{listKeys: []string{jobs.JobLogKey(1), jobs.JobLogKey(5), jobs.JobLogKey(9)}}
		hz.H.Admin.SetJobLogStore(stub)
		// JobStore left nil: the unfiltered clear must not need — nor touch — it.

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"deleted":3`))
		Expect(stub.deleteKeys).To(Equal([]string{jobs.JobLogKey(1), jobs.JobLogKey(5), jobs.JobLogKey(9)}))
	})

	It("clears only the given kind's stored logs and leaves the jobs rows intact", func() {
		database := testutil.OpenDB(GinkgoT())
		hz := testutil.NewHarnessWithDB(GinkgoT(), database)
		admUser, token := makeAdmin(hz, "joblogs_clear_kind")

		// A real jobs store with two kinds seeded — the kind filter resolves ids
		// through it (a READ). The minted admin's (unique) username suffixes the
		// kind names, keeping the shared test DB isolated across runs.
		ctx := context.Background()
		js := jobs.NewStore(database.Pool)
		hz.H.Admin.JobStore = js
		kindA := "clearkind-A-" + admUser
		kindB := "clearkind-B-" + admUser
		a1, err := js.Enqueue(ctx, kindA, "", nil, 1, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		a2, err := js.Enqueue(ctx, kindA, "", nil, 1, time.Time{})
		Expect(err).NotTo(HaveOccurred())
		b1, err := js.Enqueue(ctx, kindB, "", nil, 1, time.Time{})
		Expect(err).NotTo(HaveOccurred())

		// The store lists log objects for ALL three ids; only kindA's two survive
		// the filter.
		stub := &stubObjStore{listKeys: []string{jobs.JobLogKey(a1), jobs.JobLogKey(a2), jobs.JobLogKey(b1)}}
		hz.H.Admin.SetJobLogStore(stub)

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/logs?kind="+kindA, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"deleted":2`))
		Expect(stub.deleteKeys).To(ConsistOf(jobs.JobLogKey(a1), jobs.JobLogKey(a2)))
		Expect(stub.deleteKeys).NotTo(ContainElement(jobs.JobLogKey(b1)))

		// The jobs rows themselves are untouched — clearing logs is not a purge.
		var remaining int
		Expect(database.Pool.QueryRow(ctx,
			`SELECT count(*) FROM jobs WHERE kind = ANY($1)`, []string{kindA, kindB},
		).Scan(&remaining)).To(Succeed())
		Expect(remaining).To(Equal(3), "no jobs rows may be deleted by a log-clear")
	})

	It("is a clean no-op (deleted:0) when persistence is off", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_clear_nostore")

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/logs", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"deleted":0`))
	})

	It("no-ops (deleted:0) for a kind filter when the jobs subsystem is off", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		_, token := makeAdmin(hz, "joblogs_clear_kind_nostore")
		stub := &stubObjStore{listKeys: []string{jobs.JobLogKey(1)}}
		hz.H.Admin.SetJobLogStore(stub)
		// JobStore nil: a kind filter can't resolve ids, so nothing is deleted.

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/logs?kind=whatever", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"deleted":0`))
		Expect(stub.deleteKeys).To(BeEmpty())
	})

	It("rejects non-admin callers with 403 (no store work)", func() {
		hz := testutil.NewHarnessWithDB(GinkgoT(), testutil.OpenDB(GinkgoT()))
		stub := &stubObjStore{listKeys: []string{jobs.JobLogKey(1)}}
		hz.H.Admin.SetJobLogStore(stub)

		_, nonAdminToken := hz.MintUser("joblogs_clear_nonadmin")
		hz.Cfg.AdminUsers = map[string]struct{}{"secret-admin-dave": {}}

		e := jobLogsRouter(hz)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/jobs/logs", nonAdminToken, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		Expect(stub.deleteKeys).To(BeEmpty(), "gate must fire before any store work")
	})
})
