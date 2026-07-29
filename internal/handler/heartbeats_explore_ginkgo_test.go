// heartbeats_explore_ginkgo_test.go — ginkgo mirror of heartbeats_explore_test.go.
// 1:1 case map (2 stdlib TestXxx):
//   TestCollectExploreFiltersRejectsUnknown     → collectExploreFilters > "rejects non-whitelisted axis" + "rejects raw DB column name"
//   TestCollectExploreFiltersAcceptsWhitelisted → collectExploreFilters > "accepts whitelisted axes, reserved params ignored, empty → IS NULL"
package handler

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("collectExploreFilters", func() {
	It("rejects a non-whitelisted filter axis (400)", func() {
		c := ctxWithQuery("language=Go&sender=evil")
		_, aerr := collectExploreFilters(c)
		Expect(aerr).NotTo(BeNil(), "expected 400 for non-whitelisted filter axis 'sender'")
		Expect(aerr.Status).To(Equal(http.StatusBadRequest))
	})

	It("rejects a raw DB column name (400) — FE must use the FE axis name", func() {
		c := ctxWithQuery("is_write=true")
		_, aerr := collectExploreFilters(c)
		Expect(aerr).NotTo(BeNil(), "expected 400 for raw column 'is_write' (FE axis is 'isWrite')")
	})

	It("accepts whitelisted axes, ignores reserved params, maps empty value to IS NULL", func() {
		c := ctxWithQuery("groupBy=day&start=x&end=y&page=2&limit=50&entity=foo&language=Go&project=")
		filters, aerr := collectExploreFilters(c)
		Expect(aerr).To(BeNil())
		Expect(filters).To(HaveLen(2), "want 2 filters (language, project); got %+v", filters)

		var sawGoValue, sawNull bool
		for _, f := range filters {
			if f.Column == "language" && f.Value != nil && *f.Value == "Go" {
				sawGoValue = true
			}
			if f.Column == "project" && f.Value == nil {
				sawNull = true
			}
		}
		Expect(sawGoValue).To(BeTrue(), "expected language=Go equality filter")
		Expect(sawNull).To(BeTrue(), "expected project (empty) => IS NULL filter")
	})
})
