// date_defaults_ginkgo_test.go — ginkgo mirror of date_defaults_test.go.
// 1:1 case map (5 stdlib TestXxx w/ subtests → 16 Its):
//
//	TestParseTimeParam            → parseTimeParam > 3 Its (RFC3339 / date-only / empty)
//	TestDefaultWeekRange          → defaultWeekRange > 4 Its (no/no, no/end, start/no, both)
//	TestDefaultMonthRange         → defaultMonthRange > 4 Its (no/no, no/end, start/no, both)
//	TestQueryInt64                → queryInt64 > 3 Its (absent, valid, invalid)
//	TestTimeLimitDefault          → timeLimit > 2 Its (default, override)
package handler

import (
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseTimeParam", func() {
	It("parses RFC3339 into UTC", func() {
		c := ctxWithQuery("start=2026-03-15T08:30:00%2B02:00")
		got, ok := parseTimeParam(c, "start")
		Expect(ok).To(BeTrue())
		Expect(got.Location()).To(Equal(time.UTC))
		want := time.Date(2026, 3, 15, 6, 30, 0, 0, time.UTC)
		Expect(got.Equal(want)).To(BeTrue(), "got %v, want %v", got, want)
	})

	It("parses date-only into UTC-midnight", func() {
		c := ctxWithQuery("start=2026-03-15")
		got, ok := parseTimeParam(c, "start")
		Expect(ok).To(BeTrue())
		want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
		Expect(got.Equal(want)).To(BeTrue())
		Expect(got.Location()).To(Equal(time.UTC))
	})

	It("returns (zero,false) for an absent param", func() {
		c := ctxWithQuery("")
		got, ok := parseTimeParam(c, "start")
		Expect(ok).To(BeFalse())
		Expect(got.IsZero()).To(BeTrue())
	})
})

var _ = Describe("defaultWeekRange", func() {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	It("no start, no end → [now-7d, now] (floor span = 7)", func() {
		t0, t1 := defaultWeekRange(ctxWithQuery(""))
		Expect(math.Floor(spanDaysGinkgo(t0, t1))).To(BeEquivalentTo(7))
	})

	It("no start, end → [end-7d, end]", func() {
		t0, t1 := defaultWeekRange(ctxWithQuery("end=2026-06-01"))
		Expect(t1.Equal(end)).To(BeTrue())
		Expect(spanDaysGinkgo(t0, t1)).To(BeEquivalentTo(7))
	})

	It("start, no end → [start, start+7d]", func() {
		t0, t1 := defaultWeekRange(ctxWithQuery("start=2026-03-01"))
		Expect(t0.Equal(start)).To(BeTrue())
		Expect(spanDaysGinkgo(t0, t1)).To(BeEquivalentTo(7))
	})

	It("start, end → honored as-is", func() {
		t0, t1 := defaultWeekRange(ctxWithQuery("start=2026-03-01&end=2026-06-01"))
		Expect(t0.Equal(start)).To(BeTrue())
		Expect(t1.Equal(end)).To(BeTrue())
	})
})

var _ = Describe("defaultMonthRange", func() {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	It("no start, no end → [now-30d, now] (floor span = 30)", func() {
		t0, t1 := defaultMonthRange(ctxWithQuery(""))
		Expect(math.Floor(spanDaysGinkgo(t0, t1))).To(BeEquivalentTo(30))
	})

	It("no start, end → [end-30d, end]", func() {
		t0, t1 := defaultMonthRange(ctxWithQuery("end=2026-06-01"))
		Expect(t1.Equal(end)).To(BeTrue())
		Expect(spanDaysGinkgo(t0, t1)).To(BeEquivalentTo(30))
	})

	It("start, no end → [start, start+30d]", func() {
		t0, t1 := defaultMonthRange(ctxWithQuery("start=2026-03-01"))
		Expect(t0.Equal(start)).To(BeTrue())
		Expect(spanDaysGinkgo(t0, t1)).To(BeEquivalentTo(30))
	})

	It("start, end → honored as-is", func() {
		t0, t1 := defaultMonthRange(ctxWithQuery("start=2026-03-01&end=2026-06-01"))
		Expect(t0.Equal(start)).To(BeTrue())
		Expect(t1.Equal(end)).To(BeTrue())
	})
})

var _ = Describe("queryInt64", func() {
	It("returns the default on absent param", func() {
		Expect(queryInt64(ctxWithQuery(""), "n", 42)).To(BeEquivalentTo(42))
	})
	It("parses a valid int64", func() {
		Expect(queryInt64(ctxWithQuery("n=123"), "n", 42)).To(BeEquivalentTo(123))
	})
	It("returns the default on invalid input", func() {
		Expect(queryInt64(ctxWithQuery("n=abc"), "n", 42)).To(BeEquivalentTo(42))
	})
})

var _ = Describe("timeLimit", func() {
	It("defaults to 15 when unset", func() {
		Expect(timeLimit(ctxWithQuery(""))).To(BeEquivalentTo(15))
	})
	It("respects an explicit timeLimit override", func() {
		Expect(timeLimit(ctxWithQuery("timeLimit=30"))).To(BeEquivalentTo(30))
	})
})

// spanDaysGinkgo — mirror of the stdlib file's spanDays helper. Distinct name
// avoids duplicate-symbol collision with the stdlib file compiled into the
// same test binary.
func spanDaysGinkgo(t0, t1 time.Time) float64 {
	return t1.Sub(t0).Hours() / 24
}

// -- helpers restored from stdlib partner (gaka-0vp.17) --
func spanDays(t0, t1 time.Time) float64 {
	return t1.Sub(t0).Hours() / 24
}
