package auth

import "testing"

func TestValidateUsername(t *testing.T) {
	valid := []string{"admin", "dj.daniels", "user_1", "a-b-c", "A1", "x", "root"}
	for _, n := range valid {
		if err := ValidateUsername(n); err != nil {
			t.Errorf("ValidateUsername(%q) = %v; want nil", n, err)
		}
	}

	invalid := map[string]string{
		"":                     "empty",
		"a|b":                  "cache-key delimiter '|'",
		"owner|name":           "pipe injection into CacheKey namespace",
		"a b":                  "whitespace",
		"user@example.com":     "@ (email)",
		".leading":             "leading dot",
		"trailing-":            "trailing hyphen",
		"café":                 "non-ASCII / homoglyph",
		"a\x00b":               "NUL control char",
		"a\tb":                 "tab",
		"admin/../root":        "slash / path chars",
	}
	for n, why := range invalid {
		if err := ValidateUsername(n); err == nil {
			t.Errorf("ValidateUsername(%q) = nil; want error (%s)", n, why)
		}
	}

	// length bound
	long := make([]byte, MaxUsernameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if err := ValidateUsername(string(long)); err == nil {
		t.Errorf("ValidateUsername(%d chars) = nil; want too-long error", MaxUsernameLen+1)
	}
	okLen := make([]byte, MaxUsernameLen)
	for i := range okLen {
		okLen[i] = 'a'
	}
	if err := ValidateUsername(string(okLen)); err != nil {
		t.Errorf("ValidateUsername(%d chars) = %v; want nil (at limit)", MaxUsernameLen, err)
	}
}
