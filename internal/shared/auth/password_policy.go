// Package auth — ValidatePassword enforces the boomtime password policy:
//   - at least 8 characters (rune count, not bytes — multibyte scripts get
//     equal treatment; "日本1a" is 4 characters, not 8, even though it's 8
//     bytes in UTF-8)
//   - at least one letter (unicode.IsLetter — covers non-Latin scripts too)
//   - at least one digit (unicode.IsDigit)
//
// Returns nil on pass; returns one of the sentinel errors (ErrPasswordTooShort,
// ErrPasswordNoLetter, ErrPasswordNoDigit) on fail. The error text is safe to
// surface to the user directly — it names the failed rule and nothing else.
//
// Policy rationale: this mirrors OWASP ASVS v4 §2.1.1 (minimum 8 chars) +
// §2.1.9 (composition rules are optional but a single-class-of-character
// check catches the "12345678" / "aaaaaaaa" toy passwords the red team
// found in gaka-e5e). We deliberately keep it un-draconian — the goal is to
// block trivially-cracked passwords, not to invent a rule set that pushes
// users toward reused-elsewhere passwords or sticky-note storage.
//
// See: https://owasp.org/www-project-application-security-verification-standard/
package auth

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

// MinPasswordLen is the minimum rune count for a valid password. Exported so
// callers that want to surface the number in a UI hint don't have to hard-code
// "8" and drift if we ever bump it.
const MinPasswordLen = 8

// Sentinel errors returned by ValidatePassword. Callers can use errors.Is to
// distinguish which rule tripped — useful for UI that wants to highlight the
// specific missing class of character rather than dump a generic message.
var (
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordNoLetter = errors.New("password must contain at least one letter")
	ErrPasswordNoDigit  = errors.New("password must contain at least one digit")
)

// ValidatePassword enforces the boomtime password policy. Returns nil on pass;
// returns one of the exported sentinel errors on fail. The error's Error()
// text is deliberately safe to show to the user — it names only the failed
// rule, never any internal state or the password itself.
//
// Length is counted in RUNES, not bytes. A password like "日本1a" is 8 bytes
// in UTF-8 but only 4 code points — it must be REJECTED as too short, or
// non-ASCII users get a weaker policy than ASCII users by accident.
func ValidatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	var hasLetter, hasDigit bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
		if hasLetter && hasDigit {
			// Short-circuit the moment we've cleared the composition check —
			// no need to scan the rest of the string.
			return nil
		}
	}
	if !hasLetter {
		return ErrPasswordNoLetter
	}
	// hasLetter is true but hasDigit is false (the only remaining combination
	// — if both were true we returned above; if neither, we'd hit the
	// ErrPasswordNoLetter branch first).
	return ErrPasswordNoDigit
}
