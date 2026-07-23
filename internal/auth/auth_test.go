package auth

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, salt, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != keyLen || len(salt) != saltLen {
		t.Fatalf("hash len %d salt len %d", len(hash), len(salt))
	}
	if !VerifyPassword("s3cret", hash, salt) {
		t.Fatal("VerifyPassword should accept the correct password")
	}
	if VerifyPassword("wrong", hash, salt) {
		t.Fatal("VerifyPassword should reject a wrong password")
	}
}

func TestParseAuthHeader(t *testing.T) {
	// Stored token = base64(uuid); client sends "Basic <base64(uuid)>".
	stored := ToBase64("a2c1b8f0-0000-4000-8000-000000000000")
	tkn, ok := ParseAuthHeader("Basic " + stored)
	if !ok || tkn != stored {
		t.Fatalf("ParseAuthHeader = %q,%v want %q,true", tkn, ok, stored)
	}
	if _, ok := ParseAuthHeader(""); ok {
		t.Fatal("empty header should not parse")
	}
	if _, ok := ParseAuthHeader("Bearer xyz"); ok {
		t.Fatal("non-Basic header should not parse")
	}
}

func TestParseRefreshCookie(t *testing.T) {
	v, ok := ParseRefreshCookie("foo=bar; refresh_token=abc123; baz=qux")
	if !ok || v != "abc123" {
		t.Fatalf("ParseRefreshCookie = %q,%v want abc123,true", v, ok)
	}
	if _, ok := ParseRefreshCookie("foo=bar"); ok {
		t.Fatal("missing cookie should not parse")
	}
}

// TestBurnSentinelVerify_Counter is the unit-layer probe for gaka-imm. It
// catches: BurnSentinelVerify actually runs Argon2 (the counter increments)
// AND is safe to call before HashPassword has ever been called (lazy init).
// Deleting the counter-inc line or wiring BurnSentinelVerify to a no-op both
// fail this test — so it is NOT tautological with a mock's return value.
func TestBurnSentinelVerify_Counter(t *testing.T) {
	before := SentinelVerifyCount()
	BurnSentinelVerify("any-plaintext")
	BurnSentinelVerify("another")
	after := SentinelVerifyCount()
	if after-before != 2 {
		t.Fatalf("SentinelVerifyCount delta = %d, want 2", after-before)
	}
}

// TestHashToken_SHA256_Matches_stdlib is the unit-layer probe for gaka-b5x.2.
// It catches: a future refactor swapping SHA-256 for another digest, or a
// silent base64/hex encoding change on the storage boundary. The assertion
// re-derives the expected digest from the stdlib so the test does NOT just
// re-assert HashToken's return value.
func TestHashToken_SHA256_Matches_stdlib(t *testing.T) {
	cases := []string{"", "a", "abcdefg", "the quick brown fox"}
	for _, s := range cases {
		want := sha256.Sum256([]byte(s))
		got := HashToken(s)
		if !bytes.Equal(got, want[:]) {
			t.Errorf("HashToken(%q) = %x, want %x", s, got, want[:])
		}
		if len(got) != 32 {
			t.Errorf("HashToken(%q) len = %d, want 32", s, len(got))
		}
	}
}

// TestHashToken_Deterministic: same input → same output. Catches a future
// refactor that adds a per-call random salt (which would break the dual-path
// lookup by making the DB row unfindable on the next hash).
func TestHashToken_Deterministic(t *testing.T) {
	a := HashToken("gaka-b5x-token")
	b := HashToken("gaka-b5x-token")
	if !bytes.Equal(a, b) {
		t.Fatal("HashToken must be deterministic — a random salt would break lookup")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// gaka-awh.6 (Bravo MEDIUM): argon2id params bumped to OWASP ASVS L1 2025
// floor (time=2, parallelism=1) + versioned rehash-on-login. Unit layer.
// ─────────────────────────────────────────────────────────────────────────────

// TestArgon2Params_LockedToOWASPFloor_BravoRegression pins the CURRENT
// argon2id generation to EXACT values. If someone drops argonTime back to 1
// (below the OWASP floor) or bumps argonPar back to 4 (above the OWASP
// recommendation), this test breaks LOUD. This is the marquee regression
// guard for the Bravo red-team finding.
//
// Non-tautological proof: temporarily setting argonTime = 3 in auth.go
// makes this test fail with "argonTime = 3, want 2 (OWASP ASVS L1 2025 floor)"
// — captured at implementation time.
func TestArgon2Params_LockedToOWASPFloor_BravoRegression(t *testing.T) {
	// Current-generation constants pinned to OWASP ASVS L1 2025 floor.
	if argonTime != 2 {
		t.Errorf("argonTime = %d, want 2 (OWASP ASVS L1 2025 floor)", argonTime)
	}
	if argonMem != 64*1024 {
		t.Errorf("argonMem = %d, want %d (64 MiB)", argonMem, 64*1024)
	}
	if argonPar != 1 {
		t.Errorf("argonPar = %d, want 1 (OWASP recommends =1 to keep CPU-cache contention working vs. GPU crackers)", argonPar)
	}
	if keyLen != 64 {
		t.Errorf("keyLen = %d, want 64", keyLen)
	}
	if saltLen != 64 {
		t.Errorf("saltLen = %d, want 64", saltLen)
	}

	// Per-version tables. Legacy (v1) is frozen forever — a v1 row can only
	// verify with the ORIGINAL params it was minted under.
	tv1, mv1, pv1 := argonParamsFor(ArgonVersionLegacy)
	if tv1 != 1 || mv1 != 64*1024 || pv1 != 4 {
		t.Errorf("v1 params drifted: got (t=%d, m=%d, p=%d), want (1, 65536, 4)", tv1, mv1, pv1)
	}
	tv2, mv2, pv2 := argonParamsFor(ArgonVersionCurrent)
	if tv2 != 2 || mv2 != 64*1024 || pv2 != 1 {
		t.Errorf("v2 params drifted: got (t=%d, m=%d, p=%d), want (2, 65536, 1) — OWASP ASVS L1 2025", tv2, mv2, pv2)
	}
}

// TestHashPassword_UsesCurrentParams proves HashPassword produces a hash
// under the CURRENT-generation params — not v1. We can't inspect the
// argon2id internals from a raw hash, so we verify by round-trip: a hash
// produced by HashPassword MUST verify with v2 params and MUST NOT verify
// with v1 params (different work factor → different output for the same
// plaintext+salt).
func TestHashPassword_UsesCurrentParams(t *testing.T) {
	hash, salt, err := HashPassword("bravo-medium-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPasswordWithVersion("bravo-medium-plaintext", hash, salt, ArgonVersionCurrent) {
		t.Error("HashPassword output must verify with current-generation params")
	}
	if VerifyPasswordWithVersion("bravo-medium-plaintext", hash, salt, ArgonVersionLegacy) {
		t.Error("HashPassword output must NOT verify with legacy params — that would mean HashPassword is still producing v1 hashes")
	}
}

// TestVerifyPassword_v1AndV2_BothWork is the pure-algorithm regression
// guard: each version's hash MUST verify against its own params, and MUST
// NOT verify cross-version. This is the ONLY case where unit is the right
// layer because the check is a property of the argon2id KDF itself.
//
// Bug caught: a refactor that mixes v1 memory with v2 time (or any other
// cross-wired call) fails at least one of the four assertions.
func TestVerifyPassword_v1AndV2_BothWork(t *testing.T) {
	pw := "gaka-awh-6-crossver"

	// Same plaintext, hashed twice — once at each version.
	v1Hash, v1Salt, err := HashPasswordWithVersion(pw, ArgonVersionLegacy)
	if err != nil {
		t.Fatal(err)
	}
	v2Hash, v2Salt, err := HashPasswordWithVersion(pw, ArgonVersionCurrent)
	if err != nil {
		t.Fatal(err)
	}

	// Same-version round-trip: MUST succeed.
	if !VerifyPasswordWithVersion(pw, v1Hash, v1Salt, ArgonVersionLegacy) {
		t.Error("v1 hash must verify with v1 params (round-trip)")
	}
	if !VerifyPasswordWithVersion(pw, v2Hash, v2Salt, ArgonVersionCurrent) {
		t.Error("v2 hash must verify with v2 params (round-trip)")
	}

	// Cross-version: MUST fail. Different work-factor triples produce
	// different outputs for the same (plaintext, salt), so the constant-
	// time compare must reject.
	if VerifyPasswordWithVersion(pw, v1Hash, v1Salt, ArgonVersionCurrent) {
		t.Error("v1 hash must NOT verify with v2 params — different work factor")
	}
	if VerifyPasswordWithVersion(pw, v2Hash, v2Salt, ArgonVersionLegacy) {
		t.Error("v2 hash must NOT verify with v1 params — different work factor")
	}

	// And wrong plaintext still fails at both versions.
	if VerifyPasswordWithVersion("wrong", v1Hash, v1Salt, ArgonVersionLegacy) {
		t.Error("v1: wrong password verified")
	}
	if VerifyPasswordWithVersion("wrong", v2Hash, v2Salt, ArgonVersionCurrent) {
		t.Error("v2: wrong password verified")
	}
}
