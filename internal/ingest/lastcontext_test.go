// lastcontext_test.go — unit coverage for the pure placeholder-substitution
// helpers (no DB). These pin the forward-fill semantics the ingest handler
// relies on: axis independence, strictly-before ordering, DB seed fallback,
// and the strict no-op when a batch carries no placeholder.
package ingest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

func lcPtr(s string) *string { return &s }

// hb builds a minimal heartbeat with a time and optional axis pointers.
func lcHB(t float64, project, branch, language *string) model.HeartbeatPayload {
	return model.HeartbeatPayload{TimeSent: t, Project: project, Branch: branch, Language: language}
}

var _ = Describe("batchHasLastPlaceholder", func() {
	It("is false for a placeholder-free batch (guards the strict no-op)", func() {
		batch := []model.HeartbeatPayload{
			lcHB(1, lcPtr("foo"), lcPtr("main"), lcPtr("Go")),
			lcHB(2, nil, nil, nil),
		}
		Expect(batchHasLastPlaceholder(batch)).To(BeFalse())
	})
	It("is true when any axis of any heartbeat is a placeholder", func() {
		Expect(batchHasLastPlaceholder([]model.HeartbeatPayload{
			lcHB(1, lcPtr("foo"), lcPtr("<<LAST_BRANCH>>"), nil),
		})).To(BeTrue())
	})
})

var _ = Describe("substituteLastContext", func() {
	It("resolves a placeholder to a prior real value earlier in the same batch", func() {
		foo := lcPtr("foo")
		batch := []model.HeartbeatPayload{
			lcHB(1, foo, nil, nil),
			lcHB(2, lcPtr("<<LAST_PROJECT>>"), nil, nil),
		}
		substituteLastContext(batch, nil, nil, nil)
		Expect(batch[1].Project).NotTo(BeNil())
		Expect(*batch[1].Project).To(Equal("foo"))
	})

	It("resolves from the DB seed when the batch has no prior real value", func() {
		batch := []model.HeartbeatPayload{
			lcHB(1, lcPtr("<<LAST_PROJECT>>"), lcPtr("<<LAST_BRANCH>>"), lcPtr("<<LAST_LANGUAGE>>")),
		}
		substituteLastContext(batch, lcPtr("seedP"), lcPtr("seedL"), lcPtr("seedB"))
		Expect(*batch[0].Project).To(Equal("seedP"))
		Expect(*batch[0].Language).To(Equal("seedL"))
		Expect(*batch[0].Branch).To(Equal("seedB"))
	})

	It("drops a placeholder to nil when there is no prior real value anywhere", func() {
		batch := []model.HeartbeatPayload{
			lcHB(1, lcPtr("<<LAST_PROJECT>>"), nil, nil),
		}
		substituteLastContext(batch, nil, nil, nil)
		Expect(batch[0].Project).To(BeNil())
	})

	It("treats axes independently — a real project does not seed a placeholder branch", func() {
		batch := []model.HeartbeatPayload{
			lcHB(1, lcPtr("foo"), lcPtr("main"), nil),
			lcHB(2, lcPtr("<<LAST_PROJECT>>"), lcPtr("<<LAST_BRANCH>>"), lcPtr("<<LAST_LANGUAGE>>")),
		}
		substituteLastContext(batch, nil, nil, nil)
		Expect(*batch[1].Project).To(Equal("foo"))
		Expect(*batch[1].Branch).To(Equal("main"))
		Expect(batch[1].Language).To(BeNil(), "language never had a real value -> nil")
	})

	It("uses the most recent real value strictly before, across an intervening placeholder", func() {
		batch := []model.HeartbeatPayload{
			lcHB(1, lcPtr("foo"), nil, nil),
			lcHB(2, lcPtr("<<LAST_PROJECT>>"), nil, nil),
			lcHB(3, lcPtr("bar"), nil, nil),
			lcHB(4, lcPtr("<<LAST_PROJECT>>"), nil, nil),
		}
		substituteLastContext(batch, nil, nil, nil)
		Expect(*batch[1].Project).To(Equal("foo"), "t2 sees foo (t1)")
		Expect(*batch[2].Project).To(Equal("bar"), "t3 real, untouched")
		Expect(*batch[3].Project).To(Equal("bar"), "t4 sees the newer bar (t3)")
	})

	It("forward-fills by time order even when the batch arrives out of order (ids stay in input order)", func() {
		// Input order is t2(placeholder) then t1(real); the real one is
		// chronologically first, so the placeholder must still resolve to it.
		batch := []model.HeartbeatPayload{
			lcHB(2, lcPtr("<<LAST_PROJECT>>"), nil, nil),
			lcHB(1, lcPtr("foo"), nil, nil),
		}
		substituteLastContext(batch, nil, nil, nil)
		Expect(*batch[0].Project).To(Equal("foo"))
		// Slice order is unchanged: index 0 is still the t2 element.
		Expect(batch[0].TimeSent).To(Equal(float64(2)))
		Expect(batch[1].TimeSent).To(Equal(float64(1)))
	})

	It("leaves normal heartbeats byte-identical", func() {
		p, b, l := lcPtr("foo"), lcPtr("main"), lcPtr("Go")
		batch := []model.HeartbeatPayload{lcHB(1, p, b, l)}
		substituteLastContext(batch, lcPtr("seed"), nil, nil)
		Expect(batch[0].Project).To(BeIdenticalTo(p))
		Expect(batch[0].Branch).To(BeIdenticalTo(b))
		Expect(batch[0].Language).To(BeIdenticalTo(l))
	})
})
