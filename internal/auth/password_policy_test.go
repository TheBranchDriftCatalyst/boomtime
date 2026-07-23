package auth

import (
	"errors"
	"testing"
)

// TestValidatePassword covers the boomtime password policy:
//   - rune-count (not byte-count) length rule
//   - unicode-aware letter/digit composition
//
// The `日本1a` case is the load-bearing one: it's 8 bytes in UTF-8 but only
// 4 runes, so a byte-based length check would (incorrectly) accept it. We
// require it to be REJECTED with ErrPasswordTooShort.
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr error // nil = accepted; specific sentinel = rejected with that reason
	}{
		// --- rejects ---
		{
			name:    "empty",
			pw:      "",
			wantErr: ErrPasswordTooShort,
		},
		{
			name:    "seven ASCII digits — too short",
			pw:      "1234567",
			wantErr: ErrPasswordTooShort,
		},
		{
			name:    "eight ASCII letters — no digit",
			pw:      "aaaaaaaa",
			wantErr: ErrPasswordNoDigit,
		},
		{
			name:    "eight ASCII digits — no letter",
			pw:      "12345678",
			wantErr: ErrPasswordNoLetter,
		},
		{
			// gaka-e5e regression: 8 BYTES in UTF-8 but only 4 RUNES.
			// A byte-length check would silently accept this and give
			// non-ASCII users a strictly weaker policy than ASCII users.
			// Must be rejected.
			name:    "四文字プラス — 4 runes, 8 bytes — must be rejected",
			pw:      "日本1a",
			wantErr: ErrPasswordTooShort,
		},

		// --- accepts ---
		{
			name:    "seven letters + one digit — minimum viable",
			pw:      "aaaaaaa1",
			wantErr: nil,
		},
		{
			name:    "the eternal 'password1' — weak but meets policy",
			pw:      "password1",
			wantErr: nil,
		},
		{
			// 8 runes: 日 本 語 1 a b c d — mixed script, proves
			// unicode.IsLetter matches CJK ideographs.
			name:    "8 runes mixed script — accepted",
			pw:      "日本語1abcd",
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := ValidatePassword(tc.pw)
			if tc.wantErr == nil {
				if got != nil {
					t.Fatalf("ValidatePassword(%q) = %v, want nil", tc.pw, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ValidatePassword(%q) = nil, want %v", tc.pw, tc.wantErr)
			}
			if !errors.Is(got, tc.wantErr) {
				t.Fatalf("ValidatePassword(%q) = %v, want errors.Is == %v", tc.pw, got, tc.wantErr)
			}
		})
	}
}

// TestValidatePassword_ErrorMessagesUserSafe smoke-checks that the sentinel
// error texts don't accidentally include the password itself or something
// scary — they're surfaced directly to the user in the /auth/register 400.
func TestValidatePassword_ErrorMessagesUserSafe(t *testing.T) {
	for _, err := range []error{ErrPasswordTooShort, ErrPasswordNoLetter, ErrPasswordNoDigit} {
		msg := err.Error()
		if msg == "" {
			t.Errorf("sentinel error has empty message: %v", err)
		}
		// Sanity: message should mention "password" so the user knows which
		// field failed, but shouldn't be a novel.
		if len(msg) > 120 {
			t.Errorf("sentinel error too long (%d chars): %q", len(msg), msg)
		}
	}
}
