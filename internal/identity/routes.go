// routes.go — Echo route registrations for the identity domain
// (gaka-8tn phase 4a). Extracted from internal/server/server.go's
// registerAuthRoutes (auth + password + profile + timezone + wakatime
// key) and registerMiscRoutes (avatar routes) so those functions in
// server.go collapse toward N domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 4a.
package identity

import "github.com/labstack/echo/v5"

// Register wires the identity domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence
// (registerAuthRoutes first, then avatar routes from registerMiscRoutes)
// so any test that hit these routes previously still hits them in the
// same order — Echo picks the first registered matcher for overlapping
// patterns, so preserving order preserves matching.
func Register(e *echo.Echo, h *Handler) {
	// ---- auth cluster (pre-refactor: registerAuthRoutes) --------------
	e.POST("/auth/login", h.Login)
	e.POST("/auth/register", h.Register)
	e.POST("/auth/refresh_token", h.RefreshToken)
	e.POST("/auth/logout", h.Logout)
	// OIDC (Authentik) web login (gaka-0oe.11) + account linking (gaka-b5n.4).
	// login/link 404 unless OIDC is configured; the OpenAPI auto-derive picks
	// them up.
	e.GET("/auth/login/oidc", h.LoginOIDC)
	e.GET("/auth/link/oidc", h.LinkOIDC)
	e.GET("/auth/callback/oidc", h.CallbackOIDC)
	e.GET("/api/v1/users/current/identities", h.ListIdentities)
	e.DELETE("/api/v1/users/current/identities/:provider", h.UnlinkIdentity)
	e.POST("/auth/create_api_token", h.CreateAPIToken)
	e.GET("/auth/tokens", h.ListAPITokens)
	e.DELETE("/auth/token/:id", h.DeleteToken)
	e.POST("/auth/token", h.UpdateToken)
	e.GET("/auth/users/current", h.CurrentUser)

	// Change password (gaka-6jm): auth'd, re-verifies the current password,
	// re-hashes with argon2id, and revokes every refresh token for the
	// owner so other browsers get bounced. Registered under the
	// users/current tree (not /auth/) so it uses the same access-token
	// auth as sibling /api/v1/users/current/* endpoints.
	e.POST("/api/v1/users/current/password", h.ChangePassword)

	// Public profile (gaka-6jm.1): auth'd GET/PUT for the caller's own
	// enable-toggle + slug. The PUBLIC read endpoint (see below) resolves
	// the slug and returns the scrubbed payload.
	e.GET("/api/v1/users/current/profile", h.GetPublicProfile)
	e.PUT("/api/v1/users/current/profile", h.PutPublicProfile)

	// Encrypted-at-rest imported Wakatime API key (gaka-6jm.2). GET
	// reports only {"hasSavedKey": bool} — plaintext is never returned.
	// POST persists a user-supplied key under AES-256-GCM. DELETE clears it.
	e.GET("/api/v1/users/current/wakatime_key", h.GetWakatimeKey)
	e.POST("/api/v1/users/current/wakatime_key", h.SaveWakatimeKey)
	e.DELETE("/api/v1/users/current/wakatime_key", h.DeleteWakatimeKey)

	// User IANA timezone (gaka-dg7). GET reports the raw stored value
	// (''=unset) alongside the server's 3-level-resolved effectiveTimezone
	// so the FE can render "your choice" vs "server default" and only
	// auto-detect-and-prompt when the two differ. PATCH validates via
	// time.LoadLocation and rebuilds hb_rollup_daily so the dashboard
	// fast path immediately serves user-local buckets.
	e.GET("/api/v1/users/current/timezone", h.GetTimezone)
	e.PATCH("/api/v1/users/current/timezone", h.UpdateTimezone)

	// ---- public profile + avatar (pre-refactor: registerMiscRoutes) ---
	// Public profile — resolves slug -> user, then renders a scrubbed
	// dashboard-shaped payload. UNAUTHENTICATED; the payload MUST go
	// through widget.Scrub before serialization. See profile.go.
	e.GET("/api/public/profile/:slug", h.PublicProfile)

	// gaka-9v4: per-user CHIBI avatar. Prompt-synthesis SSE is authed
	// (currently admin-gated; see user_avatar.go for the rationale).
	// Regenerate + status are self-only (resolveUser gates on token).
	// Public GET serves the ready image bytes to the profile hero.
	e.POST("/api/v1/admin/avatar/synthesize-prompt", h.SynthesizeAvatarPrompt)
	e.POST("/api/v1/users/current/avatar/regenerate", h.RegenerateAvatar)
	e.GET("/api/v1/users/current/avatar/status", h.GetAvatarStatus)
	e.GET("/api/v1/users/:username/avatar", h.UserAvatar)
}
