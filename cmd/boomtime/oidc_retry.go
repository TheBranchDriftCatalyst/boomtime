package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
)

// oidcRetryBackoff is the schedule the degraded-start retry walks, then repeats
// its last entry forever. Front-loaded because the overwhelmingly common cause
// is a boot-ordering race (the IdP, or the route to it, coming up seconds after
// we do); the long tail exists so a genuine multi-hour outage does not have us
// hammering the issuer.
var oidcRetryBackoff = []time.Duration{
	5 * time.Second, 15 * time.Second, 30 * time.Second,
	time.Minute, 2 * time.Minute, 5 * time.Minute,
}

// retryOIDCDiscovery re-attempts discovery until it succeeds or ctx is
// cancelled, then installs the real resolver in place of PendingOIDCResolver.
//
// The swap is why SetResolver/SetOIDCResolver are mutex-guarded: this runs
// concurrently with live traffic, and every request reads those globals.
func retryOIDCDiscovery(ctx context.Context, cfg *config.Config, logger *slog.Logger) {
	for attempt := 0; ; attempt++ {
		wait := oidcRetryBackoff[len(oidcRetryBackoff)-1]
		if attempt < len(oidcRetryBackoff) {
			wait = oidcRetryBackoff[attempt]
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		r, err := auth.NewOIDCResolver(ctx, cfg.OIDCIssuer, cfg.OIDCAuthorizeURL, cfg.OIDCClientID,
			cfg.OIDCClientSecret, cfg.OIDCRedirectURL, cfg.OIDCGroupToRole, cfg.OIDCAutoprovision)
		if err != nil {
			// Debug, not Warn: the boot-time ERROR already said we are degraded,
			// and a multi-hour outage should not emit a warning every 5 minutes.
			logger.Debug("OIDC discovery retry failed", "attempt", attempt+1, "err", err, "issuer", cfg.OIDCIssuer)
			continue
		}

		// Order matters: publish the link-flow instance BEFORE making it the
		// active provider, so no request can observe an active OIDC provider
		// whose login-start handler still sees a nil instance.
		auth.SetOIDCResolver(r)
		auth.SetResolver(r)
		logger.Info("OIDC discovery recovered — login is available again",
			"issuer", cfg.OIDCIssuer, "attempts", attempt+1)
		return
	}
}
