package auth

import "sync/atomic"

// Process-global standalone-owner switch (boom-zp2s books-standalone).
//
// The STANDALONE catalyst-books binary (cmd/catalyst-books) is a self-hosted,
// single-tenant deployment with a books-only database and NO auth surface — no
// login, no OIDC, no tokens, no users-model. Every books handler still resolves
// its caller through apihelpers.Identify*, which normally hits the DB (token /
// cookie → users row). With no auth stack that lookup has nothing to resolve
// against, so the standalone main() pins ONE fixed owner here at boot and
// apihelpers.Identify short-circuits to a synthetic all-caps Identity for it —
// WITHOUT any token/cookie/DB round-trip.
//
// This mirrors userModelEnabled (usermodel_flag.go): one process-global set once
// at startup rather than threaded through every handler's config. The default is
// the empty pointer, so every code path that never sets it — the entire boomtime
// HOST binary and the whole existing test suite — behaves EXACTLY as before
// (StandaloneOwner() returns false, no short-circuit ever fires). Only the
// standalone books main() calls SetStandaloneOwner, so the host path is provably
// untouched.
var standaloneOwner atomic.Pointer[string]

// SetStandaloneOwner pins the single fixed owner for standalone single-tenant
// mode. Called once at boot by cmd/catalyst-books from BOOM_STANDALONE_OWNER
// (default "owner"). An empty string clears it (no short-circuit) so a caller
// can't accidentally activate single-owner mode with a blank name.
func SetStandaloneOwner(owner string) {
	if owner == "" {
		standaloneOwner.Store(nil)
		return
	}
	o := owner
	standaloneOwner.Store(&o)
}

// StandaloneOwner reports the pinned single-tenant owner. ok is false in the
// default (host / tests) case where SetStandaloneOwner was never called with a
// non-empty name, in which case apihelpers.Identify* takes the normal
// token/cookie resolution path.
func StandaloneOwner() (string, bool) {
	p := standaloneOwner.Load()
	if p == nil {
		return "", false
	}
	return *p, true
}
