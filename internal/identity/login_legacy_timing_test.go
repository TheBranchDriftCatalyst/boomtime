// login_legacy_timing_test.go — regression for the boom-imm follow-up found in
// the 2026-09-06 audit (internal/shared/auth/auth.go).
//
// boom-imm closed the user-enumeration oracle by burning an argon2id sentinel
// on the "no such user" branch of Login — but the sentinel burns CURRENT-
// generation params (v2: t=2, p=1) while a not-yet-rehashed row verifies with
// LEGACY params (v1: t=1, p=4). Two passes over 64 MiB on one lane against one
// pass over 64 MiB across four lanes is a ~7x wall-clock difference (measured:
// ~9 ms vs ~65 ms on this machine), so an existing pre-migration account still
// answered dramatically FASTER than a nonexistent one. That is exactly the
// oracle boom-imm closed, still open for every dormant account — precisely the
// ones an attacker probes, since a v1 row only becomes v2 by logging in.
//
// The existing boom-imm spec cannot catch this: it compares an unknown user
// against a CURRENT-generation user, where the costs match by construction.
//
// This spec times the branches with min-of-N (the minimum sample is the one
// least polluted by scheduler noise, which only ever adds time) and allows a
// generous margin — the defect it guards is a ~55 ms gap, not a subtle one.
package identity_test

import (
	"math"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// minDurationG returns the smallest sample — a far more stable estimator of
// true cost than the mean when other processes are competing for CPU.
func minDurationG(xs []time.Duration) time.Duration {
	best := xs[0]
	for _, x := range xs[1:] {
		if x < best {
			best = x
		}
	}
	return best
}

var _ = Describe("Login constant-time across argon generations (boom-imm follow-up)", func() {
	It("does not answer faster for a LEGACY-hashed account than for an unknown one", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		// A pre-migration row: argon_version=1, hashed with v1 params.
		legacyUser := "timing_legacy_v1_g"
		plantLegacyUserG(hz, legacyUser, "test1234")

		// Warm the sentinel + the argon working set so the first sample is not
		// an outlier for reasons unrelated to the params.
		auth.BurnSentinelVerify("prime")

		const N = 8
		unknownTimes := make([]time.Duration, N)
		legacyTimes := make([]time.Duration, N)

		for i := 0; i < N; i++ {
			start := time.Now()
			rec := doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
				"username": "no_such_user_legacy_probe_g",
				"password": "whatever-plaintext",
			})
			unknownTimes[i] = time.Since(start)
			Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))

			// Wrong password on purpose: a SUCCESSFUL login would rehash the
			// row to v2 and destroy the very condition under test.
			start = time.Now()
			rec = doJSONReqG(e, http.MethodPost, "/auth/login", "", map[string]string{
				"username": legacyUser,
				"password": "wrong-password-xyz",
			})
			legacyTimes[i] = time.Since(start)
			Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
		}

		// The row must still be v1 — otherwise this spec proved nothing.
		_, ver := readUserRowG(hz, legacyUser)
		Expect(ver).To(Equal(auth.ArgonVersionLegacy),
			"legacy row was upgraded mid-spec; the timing comparison is meaningless")

		minUnknown := minDurationG(unknownTimes)
		minLegacy := minDurationG(legacyTimes)
		delta := time.Duration(math.Abs(float64(minUnknown - minLegacy)))
		GinkgoWriter.Printf("legacy-generation timing: unknown-user=%s legacy-user=%s delta=%s\n",
			minUnknown, minLegacy, delta)

		Expect(delta).To(BeNumerically("<", 20*time.Millisecond),
			"argon-generation timing oracle: an unknown user costs %s but an existing LEGACY "+
				"(argon_version=1) user costs %s — delta %s. The sentinel burns v2 params while "+
				"the v1 row verifies with v1 params, so every not-yet-rehashed account is still "+
				"enumerable by timing", minUnknown, minLegacy, delta)
	})
})
