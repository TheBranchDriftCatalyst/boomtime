// cors_ginkgo_test.go — ginkgo mirror of cors_test.go (boom-0vp).
//
// 1:1 case map (3 stdlib TestXxx):
//
//	TestParseAllowedOrigins           → parseAllowedOrigins DescribeTable (11 named Entries — one per stdlib table row)
//	TestIsOriginAllowed               → isOriginAllowed DescribeTable (17 named Entries — one per stdlib table row)
//	TestIsOriginAllowed_EmptyAllowlist → isOriginAllowed(empty allowlist) DescribeTable (4 named Entries — one per origin string)
//
// The stdlib file uses shared helper silentLogger() — we call it here too
// (same package) so both suites share a single quiet slog handler.
package server

import (
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseAllowedOrigins", func() {
	DescribeTable("parses/drops entries per RFC 6454-shape rules",
		func(in string, want []string) {
			got := parseAllowedOrigins(in, silentLogger())
			if want == nil {
				// stdlib check was len-based; mirror: nil-or-empty is acceptable.
				Expect(got).To(BeEmpty())
				return
			}
			Expect(got).To(HaveLen(len(want)))
			for i := range got {
				Expect(got[i]).To(Equal(want[i]))
			}
		},
		Entry("empty string", "", nil),
		Entry("whitespace only", "   ", nil),
		Entry("single origin", "http://localhost:5173", []string{"http://localhost:5173"}),
		Entry("two origins with spaces",
			"http://localhost:5173, http://localhost:8080",
			[]string{"http://localhost:5173", "http://localhost:8080"}),
		Entry("drops trailing empty comma",
			"http://localhost:5173,",
			[]string{"http://localhost:5173"}),
		Entry("drops entry with trailing slash",
			"http://localhost:5173,http://example.com/",
			[]string{"http://localhost:5173"}),
		Entry("drops entry with path",
			"http://localhost:5173,http://example.com/api",
			[]string{"http://localhost:5173"}),
		Entry("drops entry with query",
			"http://localhost:5173,http://example.com?x=1",
			[]string{"http://localhost:5173"}),
		Entry("drops entry with userinfo",
			"http://localhost:5173,http://user:pw@example.com",
			[]string{"http://localhost:5173"}),
		Entry("drops scheme-less entry",
			"localhost:5173,http://localhost:8080",
			[]string{"http://localhost:8080"}),
		Entry("drops wildcard",
			"*,http://localhost:8080",
			[]string{"http://localhost:8080"}),
		Entry("https origin",
			"https://boomtime.example.com",
			[]string{"https://boomtime.example.com"}),
	)
})

var _ = Describe("isOriginAllowed (populated allowlist)", func() {
	allow := []string{
		"http://localhost:5173",
		"http://localhost:8080",
		"https://boomtime.example.com",
	}

	DescribeTable("exact-match, case-sensitive origin check",
		func(origin string, want bool) {
			Expect(isOriginAllowed(origin, allow)).To(Equal(want))
		},
		// allowed
		Entry("exact match localhost:5173", "http://localhost:5173", true),
		Entry("exact match localhost:8080", "http://localhost:8080", true),
		Entry("exact match prod", "https://boomtime.example.com", true),

		// denied — the classic "evil site" attack
		Entry("evil.example.com", "https://evil.example.com", false),
		Entry("evil.example.com no scheme", "evil.example.com", false),

		// denied — edge cases the beads issue called out
		Entry("null origin (sandboxed iframe / file://)", "null", false),
		Entry("empty origin", "", false),

		// denied — scheme mismatch (http vs https)
		Entry("scheme mismatch https on http entry", "https://localhost:5173", false),
		Entry("scheme mismatch http on https entry", "http://boomtime.example.com", false),

		// denied — port mismatch
		Entry("port mismatch 5174", "http://localhost:5174", false),
		Entry("port mismatch none where entry has port", "http://localhost", false),

		// denied — subdomain attack (no suffix matching)
		Entry("subdomain of allowed", "https://sub.boomtime.example.com", false),
		Entry("registered-hostile-tld", "https://boomtime.example.com.evil.com", false),

		// denied — case sensitivity
		Entry("case mismatch scheme", "HTTP://localhost:5173", false),
		Entry("case mismatch host", "http://LOCALHOST:5173", false),

		// denied — trailing slash / path
		Entry("trailing slash", "http://localhost:5173/", false),
		Entry("path", "http://localhost:5173/foo", false),
	)
})

var _ = Describe("isOriginAllowed (empty allowlist)", func() {
	// Mirrors TestIsOriginAllowed_EmptyAllowlist: an empty allowlist denies
	// EVERY origin (including the empty one).
	DescribeTable("empty allowlist denies every origin",
		func(origin string) {
			Expect(isOriginAllowed(origin, nil)).To(BeFalse())
		},
		Entry("empty string", ""),
		Entry("null literal", "null"),
		Entry("http://localhost:5173", "http://localhost:5173"),
		Entry("https://evil.example.com", "https://evil.example.com"),
	)
})

// -- helpers restored from stdlib partner (boom-0vp.17) --
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
