// handler_helpers_test.go — internal-package (package handler) coverage
// for the small helpers that remain on the composition facade after
// gaka-8tn phase 8: statsCacheTTL env parsing + the setter methods
// (SetLabelImagesWorker / SetImageJobQueue).
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
		Expect(statsCacheTTL()).To(Equal(30*time.Second),
			"unset env MUST default to 30s (the FE dashboards depend on this refresh cadence)")
	})

	It("returns the parsed seconds when BOOM_STATS_CACHE_TTL is a non-negative int", func() {
		DeferCleanup(func(prev string) { _ = os.Setenv("BOOM_STATS_CACHE_TTL", prev) }, os.Getenv("BOOM_STATS_CACHE_TTL"))
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "5")
		Expect(statsCacheTTL()).To(Equal(5 * time.Second))
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "0")
		Expect(statsCacheTTL()).To(Equal(0*time.Second),
			"BOOM_STATS_CACHE_TTL=0 MUST disable caching (documented behavior)")
	})

	It("falls back to 30s when BOOM_STATS_CACHE_TTL is negative or non-numeric (fail-safe)", func() {
		DeferCleanup(func(prev string) { _ = os.Setenv("BOOM_STATS_CACHE_TTL", prev) }, os.Getenv("BOOM_STATS_CACHE_TTL"))
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "not-a-number")
		Expect(statsCacheTTL()).To(Equal(30*time.Second),
			"non-numeric env MUST NOT panic — must fall back to the 30s default")
		_ = os.Setenv("BOOM_STATS_CACHE_TTL", "-1")
		Expect(statsCacheTTL()).To(Equal(30*time.Second),
			"negative env MUST be rejected (would break the TTL cache invariants)")
	})
})

// gaka-zp2s: the SetLabelImagesWorker / SetImageJobQueue post-construction-setter
// invariants moved with those setters onto boomtime.Module (its admin handler); see
// internal/boomtime/admin.
