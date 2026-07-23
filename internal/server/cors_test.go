// gaka-n5r: CORS allowlist unit tests. Covers the two building blocks in
// cors.go: parseAllowedOrigins (env-var parser) and isOriginAllowed (per-request
// check). Integration with echo's middleware is exercised at the running-server
// level by the curl checks documented in the beads issue — we don't spin up the
// full server here because the middleware is a thin wrapper around
// isOriginAllowed.
package server

import (
	"io"
	"log/slog"
	"testing"
)

// silentLogger discards output so test runs stay quiet. We still pass a real
// slog.Logger because parseAllowedOrigins logs WARN on invalid entries and we
// want to exercise that code path.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty string", "", nil},
		{"whitespace only", "   ", nil},
		{"single origin", "http://localhost:5173", []string{"http://localhost:5173"}},
		{
			"two origins with spaces",
			"http://localhost:5173, http://localhost:8080",
			[]string{"http://localhost:5173", "http://localhost:8080"},
		},
		{
			"drops trailing empty comma",
			"http://localhost:5173,",
			[]string{"http://localhost:5173"},
		},
		{
			"drops entry with trailing slash",
			"http://localhost:5173,http://example.com/",
			[]string{"http://localhost:5173"},
		},
		{
			"drops entry with path",
			"http://localhost:5173,http://example.com/api",
			[]string{"http://localhost:5173"},
		},
		{
			"drops entry with query",
			"http://localhost:5173,http://example.com?x=1",
			[]string{"http://localhost:5173"},
		},
		{
			"drops entry with userinfo",
			"http://localhost:5173,http://user:pw@example.com",
			[]string{"http://localhost:5173"},
		},
		{
			"drops scheme-less entry",
			"localhost:5173,http://localhost:8080",
			[]string{"http://localhost:8080"},
		},
		{
			"drops wildcard",
			"*,http://localhost:8080",
			[]string{"http://localhost:8080"},
		},
		{
			"https origin",
			"https://boomtime.example.com",
			[]string{"https://boomtime.example.com"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAllowedOrigins(tc.in, silentLogger())
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestIsOriginAllowed(t *testing.T) {
	allow := []string{
		"http://localhost:5173",
		"http://localhost:8080",
		"https://boomtime.example.com",
	}

	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		// allowed
		{"exact match localhost:5173", "http://localhost:5173", true},
		{"exact match localhost:8080", "http://localhost:8080", true},
		{"exact match prod", "https://boomtime.example.com", true},

		// denied — the classic "evil site" attack
		{"evil.example.com", "https://evil.example.com", false},
		{"evil.example.com no scheme", "evil.example.com", false},

		// denied — edge cases the beads issue called out
		{"null origin (sandboxed iframe / file://)", "null", false},
		{"empty origin", "", false},

		// denied — scheme mismatch (http vs https)
		{"scheme mismatch https on http entry", "https://localhost:5173", false},
		{"scheme mismatch http on https entry", "http://boomtime.example.com", false},

		// denied — port mismatch
		{"port mismatch 5174", "http://localhost:5174", false},
		{"port mismatch none where entry has port", "http://localhost", false},

		// denied — subdomain attack (no suffix matching)
		{"subdomain of allowed", "https://sub.boomtime.example.com", false},
		{"registered-hostile-tld", "https://boomtime.example.com.evil.com", false},

		// denied — case sensitivity (browsers send lowercase host but attacker-
		// controlled fetchers can send anything, and we want strict match).
		{"case mismatch scheme", "HTTP://localhost:5173", false},
		{"case mismatch host", "http://LOCALHOST:5173", false},

		// denied — trailing slash / path
		{"trailing slash", "http://localhost:5173/", false},
		{"path", "http://localhost:5173/foo", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOriginAllowed(tc.origin, allow); got != tc.want {
				t.Fatalf("isOriginAllowed(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

// TestIsOriginAllowed_EmptyAllowlist locks in the behavior that an empty
// allowlist denies EVERY origin (including the empty one). server.New() is
// responsible for substituting dev defaults BEFORE calling isOriginAllowed, so
// if we ever reach the check with an empty list, the safe answer is "no".
func TestIsOriginAllowed_EmptyAllowlist(t *testing.T) {
	for _, origin := range []string{
		"",
		"null",
		"http://localhost:5173",
		"https://evil.example.com",
	} {
		if isOriginAllowed(origin, nil) {
			t.Fatalf("empty allowlist must deny %q", origin)
		}
	}
}
