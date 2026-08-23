// label_images_unit_test.go — boom-d6x.handler: in-package unit tests for
// LabelImage's empty-id guard (the branch is unreachable via the wired
// route because echo drops routes with a blank param segment).
//
// Named invariant:
//
//	"blank :id short-circuits to 400 before any DB lookup" — proves the
//	fail-fast guard fires. Only reachable through a directly-constructed
//	echo.Context (a synthetic request path that satisfies the route but
//	leaves the param blank at handler entry).
package admin

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LabelImage empty-id guard (in-package)", func() {
	It("returns 400 when :id is blank at handler entry", func() {
		h := &Handler{} // no DB touch expected on the empty-id branch
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/labels//image", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		// Explicitly set an empty :id param — mirrors what echo's router
		// would produce if the pattern matched a blank segment.
		c.SetPathValues(echo.PathValues{{Name: "id", Value: ""}})

		err := h.LabelImage(c)
		Expect(err).NotTo(HaveOccurred(),
			"handler should render an error envelope, not return an error")
		Expect(rec.Code).To(Equal(http.StatusBadRequest),
			"blank id must 400 before DB; got %d body=%s", rec.Code, rec.Body.String())
	})
})
