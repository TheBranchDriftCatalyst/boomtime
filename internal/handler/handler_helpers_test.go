// handler_helpers_test.go — internal-package (package handler) coverage
// for the small helpers that remain on the composition facade after
// gaka-8tn phase 8: statsCacheTTL env parsing + the three setter
// methods (SetLabelImagesWorker / SetImageJobQueue / SetBackfillJobQueue).
//
// The former resolveOwnerFromCookie / loadSpace / resolveUser / cachedJSON
// / cachedBlob coverage moved to internal/apihelpers/apihelpers_test.go
// alongside the code — the shims those tests exercised were deleted in
// phase 8 (their bodies delegated straight to apihelpers, so the assertions
// stay byte-identical against the free-function form).
package handler

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	backfilljobs "github.com/TheBranchDriftCatalyst/boomtime/internal/queue/backfilljobs"
	imagejobs "github.com/TheBranchDriftCatalyst/boomtime/internal/queue/imagejobs"
	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/worker/labelimages"
)

var _ = Describe("statsCacheTTL (BOOM_STATS_CACHE_TTL)", func() {
	It("defaults to 30s when the env var is unset", func() {
		DeferCleanup(func(prev string) {
			if prev == "" {
				_ = os.Unsetenv("BOOM_STATS_CACHE_TTL")
			} else {
				_ = os.Setenv("BOOM_STATS_CACHE_TTL", prev)
			}
		}, os.Getenv("BOOM_STATS_CACHE_TTL"))
		_ = os.Unsetenv("BOOM_STATS_CACHE_TTL")
		Expect(statsCacheTTL()).To(Equal(30 * time.Second),
			"unset env MUST default to 30s (the FE dashboards depend on this refresh cadence)")
	})

	It("returns the parsed seconds when BOOM_STATS_CACHE_TTL is a non-negative int", func() {
		DeferCleanup(func(prev string) { _ = os.Setenv("BOOM_STATS_CACHE_TTL", prev) }, os.Getenv("BOOM_STATS_CACHE_TTL"))
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "5")
		Expect(statsCacheTTL()).To(Equal(5 * time.Second))
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "0")
		Expect(statsCacheTTL()).To(Equal(0 * time.Second),
			"BOOM_STATS_CACHE_TTL=0 MUST disable caching (documented behavior)")
	})

	It("falls back to 30s when BOOM_STATS_CACHE_TTL is negative or non-numeric (fail-safe)", func() {
		DeferCleanup(func(prev string) { _ = os.Setenv("BOOM_STATS_CACHE_TTL", prev) }, os.Getenv("BOOM_STATS_CACHE_TTL"))
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "not-a-number")
		Expect(statsCacheTTL()).To(Equal(30 * time.Second),
			"non-numeric env MUST NOT panic — must fall back to the 30s default")
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "-1")
		Expect(statsCacheTTL()).To(Equal(30 * time.Second),
			"negative env MUST be rejected (would break the TTL cache invariants)")
	})
})

var _ = Describe("Handler post-construction setters (SetLabelImagesWorker / SetImageJobQueue / SetBackfillJobQueue)", func() {
	// The setters are 2-line assignments; the invariant here is that they
	// leave the field on the SAME Handler pointer (not a copy) — which is
	// load-bearing because cmd/boomtime constructs the handler before the
	// worker/queue exist, then wires them in place.
	It("SetLabelImagesWorker mutates the receiver's field (nil-in, non-nil-after)", func() {
		h := &Handler{}
		Expect(h.LabelImagesWorker).To(BeNil())
		w := &labelimages.Worker{}
		h.SetLabelImagesWorker(w)
		Expect(h.LabelImagesWorker).To(BeIdenticalTo(w),
			"setter MUST update the field on the SAME receiver — cmd/boomtime relies on post-construction wiring")
	})

	It("SetImageJobQueue mutates the receiver's field", func() {
		h := &Handler{}
		Expect(h.ImageJobQueue).To(BeNil())
		r := &imagejobs.Registry{}
		h.SetImageJobQueue(r)
		Expect(h.ImageJobQueue).To(BeIdenticalTo(r))
	})

	It("SetBackfillJobQueue mutates the receiver's field", func() {
		h := &Handler{}
		Expect(h.BackfillJobQueue).To(BeNil())
		r := &backfilljobs.Registry{}
		h.SetBackfillJobQueue(r)
		Expect(h.BackfillJobQueue).To(BeIdenticalTo(r))
	})
})
