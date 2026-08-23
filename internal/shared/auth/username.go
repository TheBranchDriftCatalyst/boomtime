package auth

import "fmt"

// MaxUsernameLen bounds stored usernames. 64 is generous for humans and keeps
// the value well clear of any column/index limits.
const MaxUsernameLen = 64

// ValidateUsername enforces a conservative username policy (boom-93f.18).
//
// Both the local /auth/register path and OIDC autoprovisioning previously
// inserted a username VERBATIM with zero validation — so an IdP-supplied
// preferred_username (or a hostile registration) could contain control
// characters, whitespace, unicode homoglyphs, or the cache-key delimiter '|'.
// apihelpers.CacheKey builds "owner|name|..." keys, so a '|' in a username can
// collide another user's cache namespace (cross-user cached-payload leak).
//
// Policy: 1..MaxUsernameLen bytes, ASCII allowlist [A-Za-z0-9._-] only (which
// inherently rejects '|', spaces, control chars, and all non-ASCII / homoglyph
// input), and no leading/trailing '.' or '-'. This gates NEW username creation
// only — existing users are never re-validated, so it's behavior-preserving for
// anyone already in the DB.
func ValidateUsername(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("username must not be empty")
	}
	if len(name) > MaxUsernameLen {
		return fmt.Errorf("username must be at most %d characters", MaxUsernameLen)
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("username may contain only letters, digits, and . _ -")
		}
	}
	if c := name[0]; c == '.' || c == '-' {
		return fmt.Errorf("username must not start with %q", string(c))
	}
	if c := name[len(name)-1]; c == '.' || c == '-' {
		return fmt.Errorf("username must not end with %q", string(c))
	}
	return nil
}
