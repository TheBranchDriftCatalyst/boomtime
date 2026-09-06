package auth

// argon_equal_cost_test.go — deterministic companion to the handler-level
// timing spec (internal/identity/login_legacy_timing_test.go) for the boom-imm
// follow-up found in the 2026-09-06 audit.
//
// The defect: BurnSentinelVerify burned CURRENT-generation params (v2: t=2,
// p=1) while a not-yet-rehashed row verified with LEGACY params (v1: t=1,
// p=4) — a ~7x wall-clock difference, so an existing pre-migration account
// answered ~55 ms faster than a nonexistent one and stayed enumerable.
//
// A wall-clock assertion is the honest end-to-end proof but is noisy on a
// loaded machine. This test asserts the same invariant STRUCTURALLY: whichever
// branch Login takes, exactly ONE argon2id derivation happens per live
// generation. Equal work ⇒ equal cost, with nothing to measure.

import (
	"testing"
)

// argonDeltas returns how many derivations each live generation gained while fn
// ran.
func argonDeltas(fn func()) map[int]uint64 {
	before := ArgonComputeCountsForTest()
	fn()
	after := ArgonComputeCountsForTest()
	out := map[int]uint64{}
	for _, v := range ArgonLiveVersionsForTest() {
		out[v] = after[v] - before[v]
	}
	return out
}

func assertOnePerGeneration(t *testing.T, branch string, deltas map[int]uint64) {
	t.Helper()
	for _, v := range ArgonLiveVersionsForTest() {
		if deltas[v] != 1 {
			t.Errorf("%s: argon generation v%d derived %d time(s), want exactly 1 — "+
				"branches that skip a generation are CHEAPER than branches that don't, "+
				"which is precisely the login-timing oracle boom-imm exists to close",
				branch, v, deltas[v])
		}
	}
}

func TestArgonEqualCost_EveryLoginBranchBurnsEveryGeneration(t *testing.T) {
	const pw = "candidate-password"

	v1Hash, v1Salt, err := HashPasswordWithVersion(pw, ArgonVersionLegacy)
	if err != nil {
		t.Fatalf("hash v1: %v", err)
	}
	v2Hash, v2Salt, err := HashPasswordWithVersion(pw, ArgonVersionCurrent)
	if err != nil {
		t.Fatalf("hash v2: %v", err)
	}

	// Warm the sentinel so its one-time init isn't counted in the branch below.
	BurnSentinelVerify("prime")

	assertOnePerGeneration(t, "user-not-found (sentinel burn)",
		argonDeltas(func() { BurnSentinelVerify(pw) }))

	assertOnePerGeneration(t, "legacy row (argon_version=1)",
		argonDeltas(func() {
			if !VerifyPasswordWithVersion(pw, v1Hash, v1Salt, ArgonVersionLegacy) {
				t.Error("v1 hash failed to verify with v1 params")
			}
		}))

	assertOnePerGeneration(t, "current row (argon_version=2)",
		argonDeltas(func() {
			if !VerifyPasswordWithVersion(pw, v2Hash, v2Salt, ArgonVersionCurrent) {
				t.Error("v2 hash failed to verify with v2 params")
			}
		}))

	// An empty stored hash (OIDC-provisioned row) must ALSO cost the same — as
	// a pre-loop early return it answered instantly and re-opened the oracle
	// for exactly those accounts.
	assertOnePerGeneration(t, "empty stored hash (OIDC-provisioned row)",
		argonDeltas(func() {
			if VerifyPasswordWithVersion(pw, nil, v2Salt, ArgonVersionCurrent) {
				t.Error("empty stored hash AUTHENTICATED — boom-93f.19 guard is gone")
			}
		}))
}

// TestArgonEqualCost_PreservesCrossVersionRejection pins the property the
// equal-cost loop must not trade away: deriving at every generation must not
// make a hash verifiable under the WRONG generation.
func TestArgonEqualCost_PreservesCrossVersionRejection(t *testing.T) {
	const pw = "candidate-password"
	v1Hash, v1Salt, err := HashPasswordWithVersion(pw, ArgonVersionLegacy)
	if err != nil {
		t.Fatalf("hash v1: %v", err)
	}
	if VerifyPasswordWithVersion(pw, v1Hash, v1Salt, ArgonVersionCurrent) {
		t.Error("a v1 hash verified under v2 params — the equal-cost loop must compare "+
			"ONLY the row's own generation", v1Hash[:4])
	}
	if VerifyPasswordWithVersion("wrong-password", v1Hash, v1Salt, ArgonVersionLegacy) {
		t.Error("a wrong password verified against a v1 hash")
	}
	// Unknown/future versions keep argonParamsFor's defensive behaviour: they
	// are ATTEMPTED at current params rather than rejected without comparison.
	v2Hash, v2Salt, err := HashPasswordWithVersion(pw, ArgonVersionCurrent)
	if err != nil {
		t.Fatalf("hash v2: %v", err)
	}
	if !VerifyPasswordWithVersion(pw, v2Hash, v2Salt, 99) {
		t.Error("an unknown argon version no longer falls back to current params — a row " +
			"written by a newer binary would stop authenticating")
	}
}
