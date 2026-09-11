package auth

import (
	"context"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// PendingOIDCResolver is the active provider when BOOM_AUTH_PROVIDER=oidc but
// discovery has not succeeded yet — an unreachable issuer at boot, with a
// background retry still running.
//
// WHY THIS EXISTS. Discovery used to be fatal: the process exited, so an
// unreachable IdP took the whole application down. That is a wildly
// disproportionate blast radius, because almost nothing here actually needs the
// issuer:
//
//   - ResolveBearer (editor plugins, heartbeat ingest) resolves local API tokens
//     from the database. OIDCResolver delegates it to LocalPasswordResolver
//     verbatim — the issuer is not involved at all.
//   - ResolveCookie is an oidc_sessions row lookup. Also pure database.
//   - Only the LOGIN CALLBACK needs the issuer, for token exchange and JWKS.
//
// So an IdP outage should cost new logins, not heartbeat ingestion, badges and
// public profiles.
//
// WHY IT IS NOT JUST "FALL BACK TO LOCAL". Leaving currentResolver at its
// LocalPasswordResolver default would silently enable USERNAME+PASSWORD login on
// a deployment whose operator configured OIDC — a fail-open downgrade of the
// authentication model, invisible in the logs. ProviderName stays "oidc"
// precisely so nothing downstream (/healthz, boot logs) can mistake a degraded
// OIDC deployment for a local-auth one.
type PendingOIDCResolver struct{}

// ProviderName reports "oidc", NOT "local" — see the type comment. A degraded
// OIDC deployment is still an OIDC deployment.
func (PendingOIDCResolver) ProviderName() string { return "oidc" }

// ResolveBearer is byte-identical to the resolved OIDCResolver's: API tokens are
// database-backed under both providers. This is what keeps heartbeat ingest
// working through an IdP outage.
func (PendingOIDCResolver) ResolveBearer(ctx context.Context, database *db.DB, token string) (*Identity, *apierr.Error) {
	return LocalPasswordResolver{}.ResolveBearer(ctx, database, token)
}

// ResolveCookie shares OIDCResolver's implementation, so an established session
// survives an issuer outage instead of being logged out by it.
func (PendingOIDCResolver) ResolveCookie(ctx context.Context, database *db.DB, sessionID string) (*Identity, *apierr.Error) {
	return resolveOIDCSession(ctx, database, sessionID)
}

// CompleteLogin is the one operation that genuinely requires the issuer, so it
// is the one that fails. 503 (not 404) because this is a transient, recovering
// state: the background retry may install the real resolver at any moment.
func (PendingOIDCResolver) CompleteLogin(_ context.Context, _ *db.DB, _, _ string) (*Identity, *apierr.Error) {
	return nil, apierr.New(503, "OIDC discovery has not succeeded yet; login is temporarily unavailable", nil)
}
