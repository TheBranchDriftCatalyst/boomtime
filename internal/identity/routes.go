// routes.go — Echo route registrations for the identity domain
// (boom-8tn phase 4a). Extracted from internal/server/server.go's
// registerAuthRoutes (auth + password + profile + timezone + wakatime
// key) and registerMiscRoutes (avatar routes) so those functions in
// server.go collapse toward N domain-Register calls.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 4a.
//
// TYPED SEAM (internal/shared/apiroute): most routes here register through
// apiroute so their request/response Go TYPES are captured at the call site
// and the OpenAPI schema is generated rather than stubbed. The ones still on
// plain e.GET/e.POST are deliberate — they write their own response bytes
// (redirects, blobs, SSE, hand-rolled ETag/304) or carry an ordering
// invariant the seam's bind-first registration would break. Each such route
// is annotated inline with the reason.
package identity

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// Register wires the identity domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence
// (registerAuthRoutes first, then avatar routes from registerMiscRoutes)
// so any test that hit these routes previously still hits them in the
// same order — Echo picks the first registered matcher for overlapping
// patterns, so preserving order preserves matching.
func Register(e *echo.Echo, h *Handler) {
	// ---- auth cluster (pre-refactor: registerAuthRoutes) --------------
	//
	// Login/Register are POSTNoBody rather than POST: both run a guard
	// (provider==oidc / EnableRegistration) that MUST short-circuit BEFORE
	// the request body is read, and the seam's POST binds first. The bind
	// therefore stays inside those two handlers; the response type is still
	// captured. See auth.go for the invariants.
	apiroute.POSTNoBody(e, "/auth/login", h.Login)
	apiroute.POSTNoBody(e, "/auth/register", h.Register)
	apiroute.POSTNoBody(e, "/auth/refresh_token", h.RefreshToken)
	apiroute.NoContent(e, http.MethodPost, "/auth/logout", h.Logout)
	// OIDC (Authentik) web login (boom-0oe.11) + account linking (boom-b5n.4).
	// login/link 404 unless OIDC is configured; the OpenAPI auto-derive picks
	// them up.
	//
	// All three stay on plain echo: every success path is a 302 c.Redirect,
	// which has no JSON response type for the seam to capture.
	e.GET("/auth/login/oidc", h.LoginOIDC)
	e.GET("/auth/link/oidc", h.LinkOIDC)
	e.GET("/auth/callback/oidc", h.CallbackOIDC)
	apiroute.GET(e, "/api/v1/users/current/identities", h.ListIdentities)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/identities/:provider", h.UnlinkIdentity)
	// POSTNoBody: the body is OPTIONAL and decode errors are deliberately
	// ignored (see CreateAPIToken), which the seam's binder would turn into
	// a hard 400.
	apiroute.POSTNoBody(e, "/auth/create_api_token", h.CreateAPIToken)
	apiroute.GET(e, "/auth/tokens", h.ListAPITokens)
	apiroute.NoContent(e, http.MethodDelete, "/auth/token/:id", h.DeleteToken)
	apiroute.NoContentBody[model.TokenMetadata](e, http.MethodPost, "/auth/token", h.UpdateToken)
	apiroute.GET(e, "/auth/users/current", h.CurrentUser)

	// Change password (boom-6jm): auth'd, re-verifies the current password,
	// re-hashes with argon2id, and revokes every refresh token for the
	// owner so other browsers get bounced. Registered under the
	// users/current tree (not /auth/) so it uses the same access-token
	// auth as sibling /api/v1/users/current/* endpoints.
	apiroute.NoContentBody[changePasswordRequest](e, http.MethodPost, "/api/v1/users/current/password", h.ChangePassword)

	// Public profile (boom-6jm.1): auth'd GET/PUT for the caller's own
	// enable-toggle + slug. The PUBLIC read endpoint (see below) resolves
	// the slug and returns the scrubbed payload.
	apiroute.GET(e, "/api/v1/users/current/profile", h.GetPublicProfile)
	apiroute.PUT(e, "/api/v1/users/current/profile", h.PutPublicProfile)

	// Encrypted-at-rest imported Wakatime API key (boom-6jm.2). GET
	// reports only {"hasSavedKey": bool} — plaintext is never returned.
	// POST persists a user-supplied key under AES-256-GCM. DELETE clears it.
	apiroute.GET(e, "/api/v1/users/current/wakatime_key", h.GetWakatimeKey)
	apiroute.NoContentBody[wakatimeKeySaveRequest](e, http.MethodPost, "/api/v1/users/current/wakatime_key", h.SaveWakatimeKey)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/wakatime_key", h.DeleteWakatimeKey)

	// Per-user GitHub OAuth-App connection (boom-2ip Phase 1). The
	// status/disconnect API is ALWAYS registered — GET reports
	// {connected:false} and DELETE is a no-op when nothing is stored, and
	// the token is NEVER returned. The two /auth/github/* browser-redirect
	// routes register ONLY when the feature is fully configured
	// (Cfg.GithubConnectEnabled(): gate on + client id/secret + state signing
	// key), so with the default-off gate they simply 404 — inert.
	apiroute.GET(e, "/api/v1/users/current/github", h.GetGithubConnection)
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/github", h.DisconnectGithub)
	if h != nil && h.Cfg != nil && h.Cfg.GithubConnectEnabled() {
		// Both are 302 c.Redirect on every path — no JSON body to type.
		e.GET("/auth/github/connect", h.ConnectGithub)
		e.GET("/auth/github/callback", h.CallbackGithub)
		// boom-anh Phase 2: GitHub stats (authed cache-or-sync + public
		// cache-only). Gated with the connect routes — inert on a default boot.
		//
		// The public one writes its own body (hand-rolled ETag + 304
		// short-circuit), so it stays on plain echo.
		apiroute.GET(e, "/api/v1/users/current/github/stats", h.GetGithubStats)
		e.GET("/api/public/profile/:slug/github/stats", h.PublicGithubStats)
	}

	// catalyst-books HTTP surface (Amazon/Kindle/Audible + Hardcover + reading
	// items/work/curation/match) is registered separately by internal/books.Register
	// on the shared books.Handler (boom-zp2s Phase 2). It's mounted by the composition
	// root; book paths never overlap the identity paths, so relative order is immaterial.

	// Durable notifications (migration 00079) live on identity.Handler (domain-agnostic),
	// but stay gated on BooksEnabled() to preserve the pre-extraction registration exactly.
	// TODO(boom-zp2s): un-gate from BooksEnabled once notifications has its own gate.
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		apiroute.GET(e, "/api/v1/notifications", h.ListNotifications)
		apiroute.POSTNoBody(e, "/api/v1/notifications/read", h.MarkNotificationsRead)
	}

	// User IANA timezone (boom-dg7). GET reports the raw stored value
	// (''=unset) alongside the server's 3-level-resolved effectiveTimezone
	// so the FE can render "your choice" vs "server default" and only
	// auto-detect-and-prompt when the two differ. PATCH validates via
	// time.LoadLocation and rebuilds hb_rollup_daily so the dashboard
	// fast path immediately serves user-local buckets.
	apiroute.GET(e, "/api/v1/users/current/timezone", h.GetTimezone)
	apiroute.PATCH(e, "/api/v1/users/current/timezone", h.UpdateTimezone)

	// ---- public profile + avatar (pre-refactor: registerMiscRoutes) ---
	// Public profile — resolves slug -> user, then renders a scrubbed
	// dashboard-shaped payload. UNAUTHENTICATED; the payload MUST go
	// through widget.Scrub before serialization. See profile.go.
	//
	// Stays on plain echo: it marshals publicProfileResponse itself to hash
	// the exact bytes into an ETag and answers If-None-Match with a bodyless
	// 304, so the seam (which owns the encode) can't express it.
	e.GET("/api/public/profile/:slug", h.PublicProfile)
	// gaka social-card: UNAUTHENTICATED OpenGraph image. Renders the owner's
	// "social-card" widget → SVG → 1200×630 PNG (resvg-go, CGO-free) as the
	// og:image an unfurl fetches. Non-public/unknown slug → a generic
	// boomtime-branded card (no oracle). Public data only (widget.Scrub path).
	// c.Blob — no JSON type.
	e.GET("/api/public/profile/:slug/og.png", h.PublicProfileOGImage)

	// boom-9v4: per-user CHIBI avatar. Prompt-synthesis SSE is authed
	// (currently admin-gated; see user_avatar.go for the rationale).
	// Regenerate + status are self-only (resolveUser gates on token).
	// Public GET serves the ready image bytes to the profile hero.
	//
	// synthesize-prompt proxies an SSE stream (text/event-stream, written
	// chunk-by-chunk) and the public GET is a c.Blob — neither has a JSON
	// response type. Regenerate is apiroute.Accepted (202, no seam-side bind)
	// because it binds under the MEDIUM body cap, not the seam's SMALL one.
	e.POST("/api/v1/admin/avatar/synthesize-prompt", h.SynthesizeAvatarPrompt)
	apiroute.Accepted(e, http.MethodPost, "/api/v1/users/current/avatar/regenerate", h.RegenerateAvatar)
	apiroute.GET(e, "/api/v1/users/current/avatar/status", h.GetAvatarStatus)
	e.GET("/api/v1/users/:username/avatar", h.UserAvatar)

	// Per-user catalyst-go-jobs push stream (boom-hney.6): terminal job events
	// (e.g. avatar-render complete) for toasts. Cookie-authed in-handler.
	// WebSocket upgrade — never a JSON response.
	e.GET("/api/v1/jobs/ws", h.JobEventsWS)

	// Per-user domain-agnostic notification push stream: self-describing Events
	// (Type/Title/Body) for toasts. Cookie-authed in-handler.
	// WebSocket upgrade — never a JSON response.
	e.GET("/api/v1/notify/ws", h.NotifyWS)
}
