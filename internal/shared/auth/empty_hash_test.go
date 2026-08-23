package auth

import "testing"

// boom-93f.19: OIDC-provisioned users store an empty hashed_password (they
// authenticate via the IdP, never a local password). VerifyPasswordWithVersion
// must NEVER authenticate against an empty stored hash — for any input,
// including the empty string. Red-team confirmed this already fails closed via
// the constant-time length mismatch; the explicit guard hardens it against a
// future keyLen/format change. This test locks the invariant either way.
func TestVerifyPassword_EmptyStoredHashNeverAuthenticates(t *testing.T) {
	for _, pw := range []string{"", "anything", "hunter2", "\x00"} {
		for _, ver := range []int{1, 2} {
			if VerifyPasswordWithVersion(pw, nil, nil, ver) {
				t.Errorf("empty stored hash authenticated (pw=%q, ver=%d) — auth bypass", pw, ver)
			}
			if VerifyPasswordWithVersion(pw, []byte{}, []byte{}, ver) {
				t.Errorf("empty stored hash ([]byte{}) authenticated (pw=%q, ver=%d)", pw, ver)
			}
		}
	}
}
