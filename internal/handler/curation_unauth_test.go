// curation_unauth_test.go — unauth branch coverage for the curation
// cluster (gaka-d6x.handler). Every user-scoped handler starts with:
//
//	_, owner, aerr := h.resolveUser(c); if aerr != nil { return respondErr(c, aerr) }
//
// The happy paths are exercised in curation_http_test.go; here we pin the
// 401 branch for every handler so a regression that skips resolveUser
// (e.g. someone accidentally removes the guard) trips a test.
//
// Also covers unauth on spaces + labels admin endpoints.
package handler_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

var _ = Describe("Curation endpoints reject unauthenticated requests", func() {
	It("401s ListCuration / CreateCuration / DeleteCuration / Toggle / Affected / Preview / Apply / Purge", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		paths := []struct {
			method, path string
		}{
			{http.MethodGet, "/api/v1/users/current/curation"},
			{http.MethodPost, "/api/v1/users/current/curation"},
			{http.MethodDelete, "/api/v1/users/current/curation/1"},
			{http.MethodPost, "/api/v1/users/current/curation/1/toggle"},
			{http.MethodGet, "/api/v1/users/current/curation/1/affected"},
			{http.MethodGet, "/api/v1/users/current/curation/1/preview"},
			{http.MethodPost, "/api/v1/users/current/curation/1/apply"},
			{http.MethodPost, "/api/v1/users/current/curation/1/purge"},
		}
		for _, p := range paths {
			rec := doJSONReqG(e, p.method, p.path, "", nil)
			Expect(rec.Code).To(BeNumerically(">=", 400),
				"%s %s must reject unauth (>=400), got %d", p.method, p.path, rec.Code)
			// Not a 200 leaking data.
			Expect(rec.Code).NotTo(Equal(http.StatusOK),
				"%s %s leaked a 200 response without a token", p.method, p.path)
		}
	})
})

var _ = Describe("Space endpoints reject unauthenticated requests", func() {
	It("401s every /users/current/spaces endpoint", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		paths := []struct {
			method, path string
		}{
			{http.MethodGet, "/api/v1/users/current/spaces"},
			{http.MethodPost, "/api/v1/users/current/spaces"},
			{http.MethodGet, "/api/v1/users/current/spaces/preview?axis=project&matchValue=x"},
			{http.MethodGet, "/api/v1/users/current/spaces/1"},
			{http.MethodPatch, "/api/v1/users/current/spaces/1"},
			{http.MethodDelete, "/api/v1/users/current/spaces/1"},
			{http.MethodPost, "/api/v1/users/current/spaces/1/rules"},
			{http.MethodDelete, "/api/v1/users/current/spaces/1/rules/1"},
		}
		for _, p := range paths {
			rec := doJSONReqG(e, p.method, p.path, "", nil)
			Expect(rec.Code).To(BeNumerically(">=", 400),
				"%s %s must reject unauth, got %d", p.method, p.path, rec.Code)
			Expect(rec.Code).NotTo(Equal(http.StatusOK),
				"%s %s leaked 200 without token", p.method, p.path)
		}
	})
})

var _ = Describe("Admin label endpoints reject unauthenticated requests", func() {
	It("401s POST /admin/labels, PATCH /admin/labels/:id, DELETE /admin/labels/:id, PATCH /admin/label-gen-config, GET /admin/labels/seed.sql", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		paths := []struct {
			method, path string
		}{
			{http.MethodPost, "/api/v1/admin/labels"},
			{http.MethodPatch, "/api/v1/admin/labels/xxx"},
			{http.MethodDelete, "/api/v1/admin/labels/xxx"},
			{http.MethodPatch, "/api/v1/admin/label-gen-config"},
			{http.MethodGet, "/api/v1/admin/labels/seed.sql"},
		}
		for _, p := range paths {
			rec := doJSONReqG(e, p.method, p.path, "", nil)
			Expect(rec.Code).To(BeNumerically(">=", 400),
				"%s %s must reject unauth, got %d", p.method, p.path, rec.Code)
		}
	})
})
