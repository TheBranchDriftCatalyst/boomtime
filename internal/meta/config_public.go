// Public, unauthenticated client-config endpoint (gaka-93f.1.1).
//
// Boomtime had no way for the frontend to learn which server-side flags are
// active — registration open/closed was only discoverable by POSTing to
// /auth/register and catching a 403, and there was no signal at all for the
// auth provider, billing, or beta-preview switches. GET /api/v1/config/public
// fills that gap so the FE can branch the onboarding/signup CTA (local
// register vs "Continue with Authentik"), show/hide the billing surface, and
// honor the server-side kill switch for the beta onboarding preview.
//
// Response is INTENTIONALLY non-sensitive: only booleans + the provider name.
// No secrets, no per-user data, no admin-only flags. Same public-transparency
// posture as /healthz and /api/v1/version — safe to expose on the open net.
package meta

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// PublicConfigResponse is the JSON shape returned by GET /api/v1/config/public.
// Every field is a capability/mode advertisement the FE needs at boot to pick
// the right auth + onboarding flow. Keys are snake_case to match the rest of
// the boomtime JSON API.
type PublicConfigResponse struct {
	// RegistrationEnabled mirrors BOOM_ENABLE_REGISTRATION — whether the local
	// POST /auth/register path accepts new users. When AuthProvider is "oidc"
	// this is moot (signup flows through Authentik) but still reported.
	RegistrationEnabled bool `json:"registration_enabled"`

	// AuthProvider is "local" (username+password) or "oidc" (Authentik). The
	// FE swaps the signup CTA and login form on this value.
	AuthProvider string `json:"auth_provider"`

	// OIDCEnabled is a convenience derivation of AuthProvider == "oidc" so the
	// FE doesn't string-compare.
	OIDCEnabled bool `json:"oidc_enabled"`

	// BillingEnabled advertises whether the Stripe SaaS billing surface is
	// live (pricing page, upgrade CTA, billing settings). Off until the
	// billing subsystem ships (gaka-93f Phase 4).
	BillingEnabled bool `json:"billing_enabled"`

	// BetaFlags is a map of server-advertised beta-feature kill switches. The
	// FE checks these before honoring the corresponding ?enable_beta_* URL
	// flag, so an operator can disable a preview instance-wide. Currently:
	//   user_registration — the beta onboarding preview flow (gaka-93f.1).
	BetaFlags map[string]bool `json:"beta_flags"`

	// GithubConnectEnabled advertises whether the per-user GitHub connect
	// feature (gaka-2ip Phase 1) is live — true ONLY when the gate is on AND
	// the OAuth-App credentials + state signing key are configured. The FE
	// GitHubConnectCard renders nothing when this is false, so the whole
	// surface stays inert until an operator provisions the secrets.
	GithubConnectEnabled bool `json:"github_connect_enabled"`
}

// PublicConfig: GET /api/v1/config/public — unauthenticated, always JSON.
// Reads only from Cfg; no DB access, so it stays fast and can't degrade.
func (h *Handler) PublicConfig(c *echo.Context) error {
	return c.JSON(http.StatusOK, PublicConfigResponse{
		RegistrationEnabled: h.Cfg.EnableRegistration,
		AuthProvider:        h.Cfg.AuthProvider,
		OIDCEnabled:         h.Cfg.OIDCEnabled(),
		BillingEnabled:      h.Cfg.FeatureBilling,
		BetaFlags: map[string]bool{
			"user_registration": h.Cfg.BetaUserRegistration,
		},
		GithubConnectEnabled: h.Cfg.GithubConnectEnabled(),
	})
}
