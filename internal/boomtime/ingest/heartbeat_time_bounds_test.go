// heartbeat_time_bounds_test.go — in-package unit coverage for
// validateHeartbeatTimes' boundary behaviour (2026-09-06 audit).
//
// The HTTP-level regression lives in heartbeat_time_validation_test.go; this
// file pins the exact accept/reject boundary, which an HTTP test cannot do
// without a clock seam (validateHeartbeatTimes takes `now` explicitly so the
// future-skew edge is testable at all).
package ingest

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

var _ = Describe("validateHeartbeatTimes bounds", func() {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	beat := func(unix float64) model.HeartbeatPayload {
		return model.HeartbeatPayload{TimeSent: unix, Entity: "a.go", Type: model.FileType}
	}

	DescribeTable("accept/reject",
		func(ts float64, wantOK bool, why string) {
			err := validateHeartbeatTimes([]model.HeartbeatPayload{beat(ts)}, now)
			if wantOK {
				Expect(err).To(BeNil(), why)
			} else {
				Expect(err).NotTo(BeNil(), why)
				Expect(err.Status).To(Equal(400))
			}
		},
		Entry("omitted `time` binds to the float64 zero", float64(0), false,
			"0 is the exact value an absent JSON key produces — the whole point of the guard"),
		Entry("negative unix seconds", float64(-1), false, "pre-epoch is never a real heartbeat"),
		Entry("1970-01-02 (positive but below the floor)", float64(86400), false,
			"a small positive epoch is just as poisonous as 0: it still drags refreshRollup back to 1970"),
		Entry("one second below the 2000-01-01 floor", float64(946684799), false, "floor is exclusive below"),
		Entry("exactly the 2000-01-01 floor", float64(946684800), true, "floor itself is accepted"),
		Entry("a normal recent beat", float64(now.Add(-time.Hour).Unix()), true, "ordinary ingest must pass"),
		Entry("23h into the future (clock skew)", float64(now.Add(23*time.Hour).Unix()), true,
			"a drifting laptop clock must not have its work rejected"),
		Entry("25h into the future", float64(now.Add(25*time.Hour).Unix()), false, "beyond the skew allowance"),
		Entry("millisecond epoch sent as seconds", float64(now.UnixMilli()), false,
			"the classic ms-vs-s bug lands in the year 57000 and must be caught"),
	)

	It("reports the index of the first offending element in a batch", func() {
		good := beat(float64(now.Add(-time.Minute).Unix()))
		err := validateHeartbeatTimes([]model.HeartbeatPayload{good, good, beat(0)}, now)
		Expect(err).NotTo(BeNil())
		Expect(err.Message).To(ContainSubstring("heartbeat[2]"))
	})

	It("accepts an empty batch (no beats, nothing to validate)", func() {
		Expect(validateHeartbeatTimes(nil, now)).To(BeNil())
	})
})
