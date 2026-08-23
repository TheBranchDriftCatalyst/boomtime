// curation_unauth_test.go — unauth branch coverage for the curation
// cluster (boom-d6x.handler). Every user-scoped handler starts with:
//
//	_, owner, aerr := h.resolveUser(c); if aerr != nil { return respondErr(c, aerr) }
//
// The happy paths are exercised in curation_http_test.go; here we pin the
// exact status a missing- or invalid-token request returns for every
// endpoint. Two branches are exercised separately (never collapsed into
// "any 4xx or 5xx"), because a regression that panics before the auth
// check and returns 500 would slip through a `>=400` assertion:
//
//   - Missing Authorization header → apierr.MissingAuth() → 400.
//   - Present-but-unknown token   → apierr.InvalidToken() → 401.
//
// Also covers unauth on spaces + labels admin endpoints.
package curation_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Curation endpoints reject unauthenticated requests", func() {
	It("returns exactly 400 (MissingAuth) with NO Authorization header, on every endpoint", func() {
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
			// Pin the EXACT contract — a regression that returns 500
			// (e.g. panicking before the auth check) or 200 would trip
			// this assertion instead of quietly passing `>= 400`.
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"%s %s: MissingAuth must be exactly 400 (never 500 masking an auth check)",
				p.method, p.path)
			// The body must NOT contain any partial data leaks — the
			// handler must reject BEFORE serializing anything from a
			// user-scoped resource.
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"rules"`),
				"%s %s: unauth body leaked 'rules' key (partial serialization?)", p.method, p.path)
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"spaces"`),
				"%s %s: unauth body leaked 'spaces' key (partial serialization?)", p.method, p.path)
		}
	})

	It("returns exactly 401 (InvalidToken) with an unknown-but-well-formed Authorization header", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		paths := []struct {
			method, path string
		}{
			{http.MethodGet, "/api/v1/users/current/curation"},
			{http.MethodPost, "/api/v1/users/current/curation"},
			{http.MethodPost, "/api/v1/users/current/curation/1/apply"},
			{http.MethodPost, "/api/v1/users/current/curation/1/purge"},
		}
		// A syntactically valid but unknown token — MissingAuth fires only
		// when the header is absent; a lookup miss returns InvalidToken (401).
		bogus := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		for _, p := range paths {
			rec := doJSONReqG(e, p.method, p.path, bogus, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusUnauthorized),
				"%s %s: unknown token must be exactly 401, never 500", p.method, p.path)
		}
	})
})

var _ = Describe("Space endpoints reject unauthenticated requests", func() {
	It("returns exactly 400 (MissingAuth) with no Authorization on every endpoint", func() {
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
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"%s %s: MissingAuth must be exactly 400", p.method, p.path)
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"spaces"`),
				"%s %s: unauth body leaked 'spaces' key", p.method, p.path)
		}
	})

	It("returns exactly 401 (InvalidToken) with a bogus token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		bogus := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		paths := []struct {
			method, path string
		}{
			{http.MethodGet, "/api/v1/users/current/spaces"},
			{http.MethodPost, "/api/v1/users/current/spaces"},
		}
		for _, p := range paths {
			rec := doJSONReqG(e, p.method, p.path, bogus, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusUnauthorized),
				"%s %s: unknown token must be exactly 401", p.method, p.path)
		}
	})
})

var _ = Describe("Admin label endpoints reject unauthenticated requests", func() {
	It("returns exactly 400 (MissingAuth) with no Authorization on every admin endpoint", func() {
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
			Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
				"%s %s: MissingAuth must be exactly 400 (not 500)", p.method, p.path)
		}
	})

	It("returns exactly 401 (InvalidToken) on admin endpoints with a bogus token", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		bogus := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		paths := []struct {
			method, path string
		}{
			{http.MethodPost, "/api/v1/admin/labels"},
			{http.MethodGet, "/api/v1/admin/labels/seed.sql"},
		}
		for _, p := range paths {
			rec := doJSONReqG(e, p.method, p.path, bogus, nil)
			Expect(rec).To(testutil.HaveStatus(http.StatusUnauthorized),
				"%s %s: unknown token must be exactly 401", p.method, p.path)
		}
	})
})
