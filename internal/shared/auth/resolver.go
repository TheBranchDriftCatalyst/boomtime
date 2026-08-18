// resolver.go — the pluggable auth-provider boundary (gaka-0oe.2).
//
// IdentityResolver abstracts "how do we turn a request credential into an
// Identity". LocalPasswordResolver (today) wraps the username+password + API
// token + refresh cookie world with ZERO behavior change; a future
// OIDCResolver (gaka-0oe.11) is the second implementation, selected at boot via
// BOOM_AUTH_PROVIDER. apihelpers.Identify* delegates to CurrentResolver(), so
// the eventual OIDC swap is a one-line SetResolver at boot — no handler touched.
//
// Methods take *db.DB per-call (rather than the resolver holding a fixed pool)
// to match the codebase's per-request-db convention and keep the resolver a
// cheap stateless value that CurrentResolver() can hand back without wiring a
// pool into a process global. This part of the substrate stays dependency-light
// for the eventual catalyst-auth extraction.
package auth

import (
	"context"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// IdentityResolver is the auth-provider boundary. LocalPasswordResolver +
// (future) OIDCResolver implement it.
type IdentityResolver interface {
	// ProviderName is "local" or "oidc" — for /healthz + boot logs.
	ProviderName() string
	// ResolveBearer maps a raw API-token value to an Identity. The caller
	// (apihelpers) parses the Authorization header and passes the token.
	ResolveBearer(ctx context.Context, database *db.DB, token string) (*Identity, *apierr.Error)
	// ResolveCookie maps a refresh-cookie value to an Identity.
	ResolveCookie(ctx context.Context, database *db.DB, refresh string) (*Identity, *apierr.Error)
	// CompleteLogin handles an OIDC callback (code+state → Identity). Only the
	// OIDC provider implements it; LocalPasswordResolver returns 404 (local
	// login is username+password through the /auth/login handler).
	CompleteLogin(ctx context.Context, database *db.DB, code, state string) (*Identity, *apierr.Error)
}

// LocalPasswordResolver is today's behavior, unchanged: API tokens +
// refresh-cookie sessions resolve to owners via the hashed-token tables, then
// build an Identity through the user-model substrate (all-caps when the flag is
// off; real role/capabilities + disabled-fail-closed when on).
type LocalPasswordResolver struct{}

func (LocalPasswordResolver) ProviderName() string { return "local" }

func (LocalPasswordResolver) ResolveBearer(ctx context.Context, database *db.DB, token string) (*Identity, *apierr.Error) {
	owner, ok, err := database.GetUserByToken(ctx, token)
	if err != nil {
		return nil, apierr.Generic()
	}
	if !ok {
		return nil, apierr.InvalidToken()
	}
	return resolveIdentity(ctx, database, owner)
}

func (LocalPasswordResolver) ResolveCookie(ctx context.Context, database *db.DB, refresh string) (*Identity, *apierr.Error) {
	owner, ok, err := database.GetUserByRefreshToken(ctx, refresh)
	if err != nil {
		return nil, apierr.Generic()
	}
	if !ok {
		return nil, apierr.ExpiredRefreshToken()
	}
	return resolveIdentity(ctx, database, owner)
}

func (LocalPasswordResolver) CompleteLogin(_ context.Context, _ *db.DB, _, _ string) (*Identity, *apierr.Error) {
	return nil, apierr.NotFound("OIDC callback is not available for the local auth provider")
}

// resolveIdentity turns an already-resolved owner into an Identity, applying the
// user-model gate: flag off → all-capability identity (byte-identical to
// pre-substrate); flag on → real role/capabilities with disabled accounts
// failing closed. This is the single source of truth both resolve paths share
// (formerly apihelpers.identityFor).
func resolveIdentity(ctx context.Context, database *db.DB, owner string) (*Identity, *apierr.Error) {
	if !UserModelEnabled() {
		return AllCapsIdentity(owner), nil
	}
	full, err := database.GetUserFullByName(ctx, owner)
	if err != nil {
		return nil, apierr.Generic()
	}
	// Token resolved to an owner but the row is gone, or the account is
	// disabled → fail closed (invalid session).
	if full == nil || full.DisabledAt != nil {
		return nil, apierr.InvalidToken()
	}
	return BuildIdentity(owner, full.Role, full.Capabilities, false), nil
}

// currentResolver is the process-global active provider. Defaults to local so
// zero-config callers (and the entire existing test suite) behave exactly as
// before; main() calls SetResolver at boot per BOOM_AUTH_PROVIDER.
var currentResolver IdentityResolver = LocalPasswordResolver{}

// SetResolver swaps the active provider (called once at boot).
func SetResolver(r IdentityResolver) {
	if r != nil {
		currentResolver = r
	}
}

// CurrentResolver returns the active provider (never nil).
func CurrentResolver() IdentityResolver { return currentResolver }
