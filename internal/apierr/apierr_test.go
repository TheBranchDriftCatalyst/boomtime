// apierr_ginkgo_test.go — ginkgo mirror of apierr_test.go (gaka-0vp).
// 1:1 case map (2 stdlib TestXxx → 12 DescribeTable Entries + 1 It):
//   TestPredefinedErrorStatuses → predefined constructor > table of 12
//   TestNewAndError             → New + Error() > "New + Error()"
package apierr

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("predefined error constructors", func() {
	DescribeTable("returns the documented HTTP status",
		func(err *Error, want int) {
			Expect(err.Status).To(Equal(want))
		},
		Entry("MissingAuth is 400 (not 401)", MissingAuth(), http.StatusBadRequest),
		Entry("MissingQueryParam is 400", MissingQueryParam("start"), http.StatusBadRequest),
		Entry("MissingRefreshTokenCookie is 400", MissingRefreshTokenCookie(), http.StatusBadRequest),
		Entry("InvalidToken is 403", InvalidToken(), http.StatusForbidden),
		Entry("InvalidRelation is 404", InvalidRelation("u", "p"), http.StatusNotFound),
		Entry("ExpiredRefreshToken is 403", ExpiredRefreshToken(), http.StatusForbidden),
		Entry("DisabledRegistration is 403", DisabledRegistration(), http.StatusForbidden),
		Entry("UsernameExists is 409", UsernameExists("bob"), http.StatusConflict),
		Entry("RegisterError is 409", RegisterError(), http.StatusConflict),
		Entry("InvalidCredentials is 403", InvalidCredentials(), http.StatusForbidden),
		Entry("MissingGithubToken is 500", MissingGithubToken(), http.StatusInternalServerError),
		Entry("Generic is 500", Generic(), http.StatusInternalServerError),
	)
})

var _ = Describe("New + Error()", func() {
	It("preserves status / message / extra and Error() surfaces the message", func() {
		extra := "detail"
		e := New(422, "bad thing", &extra)

		Expect(e.Status).To(Equal(422))
		Expect(e.Message).To(Equal("bad thing"))
		Expect(e.Extra).NotTo(BeNil())
		Expect(*e.Extra).To(Equal("detail"))
		Expect(e.Error()).To(Equal("bad thing"))
	})
})
