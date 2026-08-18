package testutil_test

import (
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("HaveStatus matcher", func() {
	It("matches when the recorded Code equals the target", func() {
		rec := httptest.NewRecorder()
		rec.Code = 200
		Expect(rec).To(testutil.HaveStatus(200))
	})

	It("does not match when the codes differ", func() {
		rec := httptest.NewRecorder()
		rec.Code = 500
		Expect(rec).NotTo(testutil.HaveStatus(200))
	})

	It("errors clearly when handed a non-recorder", func() {
		ok, err := testutil.HaveStatus(200).Match("not a recorder")
		Expect(ok).To(BeFalse())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expects *httptest.ResponseRecorder"))
	})

	It("failure message names both the want and got status codes AND the body", func() {
		rec := httptest.NewRecorder()
		rec.Code = 500
		rec.Body.WriteString(`{"err":"boom"}`)
		msg := testutil.HaveStatus(200).FailureMessage(rec)
		Expect(msg).To(ContainSubstring("200"))
		Expect(msg).To(ContainSubstring("500"))
		Expect(msg).To(ContainSubstring(`"err":"boom"`))
	})

	It("truncates a body larger than 1 KiB with an ellipsis marker", func() {
		rec := httptest.NewRecorder()
		rec.Code = 500
		big := make([]byte, 4096)
		for i := range big {
			big[i] = 'a'
		}
		rec.Body.Write(big)
		msg := testutil.HaveStatus(200).FailureMessage(rec)
		Expect(msg).To(ContainSubstring("…(truncated)"))
	})
})

var _ = Describe("HaveHeader matcher", func() {
	It("matches when the header equals want", func() {
		rec := httptest.NewRecorder()
		rec.Header().Set("Cache-Control", "private,max-age=30")
		Expect(rec).To(testutil.HaveHeader("Cache-Control", "private,max-age=30"))
	})

	It("does not match when the header differs", func() {
		rec := httptest.NewRecorder()
		rec.Header().Set("Cache-Control", "no-store")
		Expect(rec).NotTo(testutil.HaveHeader("Cache-Control", "private,max-age=30"))
	})

	It("matches empty want against absent header (http.Header.Get returns \"\")", func() {
		rec := httptest.NewRecorder()
		Expect(rec).To(testutil.HaveHeader("X-Not-Set", ""))
	})

	It("errors clearly on the wrong actual type", func() {
		ok, err := testutil.HaveHeader("X", "y").Match(42)
		Expect(ok).To(BeFalse())
		Expect(err).To(HaveOccurred())
	})
})
