// auth_ginkgo_test.go — ginkgo mirror of auth_test.go (boom-0vp).
// 1:1 case map (7 stdlib TestXxx):
//
//	TestPasswordRoundTrip                        → HashPassword+VerifyPassword > "round trip"
//	TestParseAuthHeader                          → ParseAuthHeader > "table of 3 cases"
//	TestParseRefreshCookie                       → ParseRefreshCookie > "hit / miss"
//	TestBurnSentinelVerify_Counter               → BurnSentinelVerify > "increments counter (boom-imm)"
//	TestHashToken_SHA256_Matches_stdlib          → HashToken > "matches stdlib SHA-256"
//	TestHashToken_Deterministic                  → HashToken > "deterministic (no random salt)"
//	TestArgon2Params_LockedToOWASPFloor_BravoRegression
//	                                             → argon2 params > "pinned to OWASP ASVS L1 2025 floor"
//	TestHashPassword_UsesCurrentParams           → HashPassword > "uses current-generation params"
//	TestVerifyPassword_v1AndV2_BothWork          → VerifyPassword > "v1 and v2 same-version round-trip; cross-version fails"
package auth

import (
	"bytes"
	"crypto/sha256"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HashPassword + VerifyPassword", func() {
	It("round-trips a valid password and rejects a wrong one", func() {
		hash, salt, err := HashPassword("s3cret")
		Expect(err).NotTo(HaveOccurred())
		Expect(hash).To(HaveLen(keyLen))
		Expect(salt).To(HaveLen(saltLen))
		Expect(VerifyPassword("s3cret", hash, salt)).To(BeTrue())
		Expect(VerifyPassword("wrong", hash, salt)).To(BeFalse())
	})
})

var _ = Describe("ParseAuthHeader", func() {
	stored := ToBase64("a2c1b8f0-0000-4000-8000-000000000000")

	It("parses a well-formed Basic header", func() {
		tkn, ok := ParseAuthHeader("Basic " + stored)
		Expect(ok).To(BeTrue())
		Expect(tkn).To(Equal(stored))
	})

	It("rejects an empty header", func() {
		_, ok := ParseAuthHeader("")
		Expect(ok).To(BeFalse())
	})

	It("rejects a non-Basic header", func() {
		_, ok := ParseAuthHeader("Bearer xyz")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("ParseRefreshCookie", func() {
	It("extracts refresh_token from a cookie string", func() {
		v, ok := ParseRefreshCookie("foo=bar; refresh_token=abc123; baz=qux")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("abc123"))
	})

	It("returns !ok when refresh_token is missing", func() {
		_, ok := ParseRefreshCookie("foo=bar")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("BurnSentinelVerify (boom-imm)", func() {
	It("increments SentinelVerifyCount and runs safely before HashPassword", func() {
		before := SentinelVerifyCount()
		BurnSentinelVerify("any-plaintext")
		BurnSentinelVerify("another")
		after := SentinelVerifyCount()
		Expect(after - before).To(BeEquivalentTo(2))
	})
})

var _ = Describe("HashToken (boom-b5x.2)", func() {
	It("matches stdlib SHA-256 exactly for every fixture", func() {
		for _, s := range []string{"", "a", "abcdefg", "the quick brown fox"} {
			want := sha256.Sum256([]byte(s))
			got := HashToken(s)
			Expect(bytes.Equal(got, want[:])).To(BeTrue(),
				"input=%q got=%x want=%x", s, got, want[:])
			Expect(got).To(HaveLen(32))
		}
	})

	It("is deterministic — no random salt (would break DB lookup)", func() {
		a := HashToken("boom-b5x-token")
		b := HashToken("boom-b5x-token")
		Expect(bytes.Equal(a, b)).To(BeTrue())
	})
})

var _ = Describe("argon2 params (boom-awh.6 OWASP ASVS L1 2025 floor)", func() {
	It("pins current-generation constants", func() {
		Expect(argonTime).To(BeEquivalentTo(2),
			"argonTime must stay at OWASP floor")
		Expect(argonMem).To(BeEquivalentTo(64*1024),
			"argonMem must stay at 64 MiB")
		Expect(argonPar).To(BeEquivalentTo(1),
			"argonPar must stay at 1 (OWASP recommends =1 vs GPU crackers)")
		Expect(keyLen).To(BeEquivalentTo(64))
		Expect(saltLen).To(BeEquivalentTo(64))
	})

	It("keeps legacy (v1) params frozen — old rows must still verify", func() {
		tv1, mv1, pv1 := argonParamsFor(ArgonVersionLegacy)
		Expect(tv1).To(BeEquivalentTo(1))
		Expect(mv1).To(BeEquivalentTo(64 * 1024))
		Expect(pv1).To(BeEquivalentTo(4))
	})

	It("returns current params for ArgonVersionCurrent", func() {
		tv2, mv2, pv2 := argonParamsFor(ArgonVersionCurrent)
		Expect(tv2).To(BeEquivalentTo(2))
		Expect(mv2).To(BeEquivalentTo(64 * 1024))
		Expect(pv2).To(BeEquivalentTo(1))
	})
})

var _ = Describe("HashPassword generation", func() {
	It("produces a hash under current-generation params (not v1)", func() {
		hash, salt, err := HashPassword("bravo-medium-plaintext")
		Expect(err).NotTo(HaveOccurred())
		Expect(VerifyPasswordWithVersion("bravo-medium-plaintext", hash, salt, ArgonVersionCurrent)).To(BeTrue(),
			"HashPassword output must verify with current-generation params")
		Expect(VerifyPasswordWithVersion("bravo-medium-plaintext", hash, salt, ArgonVersionLegacy)).To(BeFalse(),
			"HashPassword output must NOT verify with legacy params")
	})
})

var _ = Describe("VerifyPasswordWithVersion", func() {
	pw := "boom-awh-6-crossver"

	It("v1 and v2 both round-trip under their own version; cross-version fails", func() {
		v1Hash, v1Salt, err := HashPasswordWithVersion(pw, ArgonVersionLegacy)
		Expect(err).NotTo(HaveOccurred())
		v2Hash, v2Salt, err := HashPasswordWithVersion(pw, ArgonVersionCurrent)
		Expect(err).NotTo(HaveOccurred())

		// Same-version.
		Expect(VerifyPasswordWithVersion(pw, v1Hash, v1Salt, ArgonVersionLegacy)).To(BeTrue())
		Expect(VerifyPasswordWithVersion(pw, v2Hash, v2Salt, ArgonVersionCurrent)).To(BeTrue())

		// Cross-version.
		Expect(VerifyPasswordWithVersion(pw, v1Hash, v1Salt, ArgonVersionCurrent)).To(BeFalse())
		Expect(VerifyPasswordWithVersion(pw, v2Hash, v2Salt, ArgonVersionLegacy)).To(BeFalse())

		// Wrong plaintext.
		Expect(VerifyPasswordWithVersion("wrong", v1Hash, v1Salt, ArgonVersionLegacy)).To(BeFalse())
		Expect(VerifyPasswordWithVersion("wrong", v2Hash, v2Salt, ArgonVersionCurrent)).To(BeFalse())
	})
})
