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
// TYPED SEAM (internal/shared/apiroute): EVERY route here registers through
// apiroute, so its request/response Go TYPES and its human documentation are
// captured at the call site and the OpenAPI operation is generated rather
// than stubbed. The non-JSON routes use the dedicated forms — Redirect for
// the OAuth/OIDC 302 entry+callback pairs, Raw for the PNG / SSE / avatar
// blobs, WebSocket for the two push streams, WritesJSON for the two handlers
// that marshal their own bytes to hash an ETag. Registering those as plain
// JSON would document a body that never arrives.
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
	// therefore stays inside those two handlers (4 KiB, BodyLimitSmall);
	// the response type is still captured. See auth.go for the invariants.
	apiroute.POSTNoBody(e, "/auth/login", h.Login).
		Doc("Password sign-in",
			"Exchanges {username, password} for a 30-minute bearer access token and sets the "+
				"HttpOnly, SameSite=Strict `refresh_token` cookie used by the cookie-authed "+
				"endpoints. Request body is capped at 4 KiB and is read only AFTER the provider "+
				"guard: when BOOM_AUTH_PROVIDER=oidc password login is disabled and this answers "+
				"403 without reading the body — sign in through /auth/login/oidc instead. "+
				"Unknown username, wrong password, and a disabled account are deliberately "+
				"indistinguishable (identical 403 'Invalid credentials' envelope, and the "+
				"unknown-user path burns the same argon2id cost) so the endpoint cannot be used "+
				"to enumerate accounts by response body or by timing. A successful sign-in "+
				"transparently re-hashes a legacy argon2 generation to the current one.").
		Tag("Auth")
	apiroute.POSTNoBody(e, "/auth/register", h.Register).
		Doc("Self-service registration",
			"Creates a user from {username, password} and immediately returns the same "+
				"{token, tokenExpiry, tokenUsername} a login would, plus the `refresh_token` "+
				"cookie. Refused with 403 'Registration is disabled' unless "+
				"BOOM_ENABLE_REGISTRATION is on — that guard runs BEFORE the 4 KiB body read, "+
				"so an over-cap body on a closed server answers 403, never 413. Password and "+
				"username are validated against the shared policy (400 with a user-safe reason); "+
				"a taken username is 409.").
		Tag("Auth")
	apiroute.POSTNoBody(e, "/auth/refresh_token", h.RefreshToken).
		Doc("Mint a fresh access token",
			"Reads the `refresh_token` cookie (no request body, no Authorization header) and "+
				"returns a new 30-minute bearer token. Under the local provider it also rotates "+
				"the refresh cookie. Under BOOM_AUTH_PROVIDER=oidc it mints ONLY the bearer and "+
				"leaves the cookie alone — the server-side OIDC session stays the single "+
				"revocable credential — and best-effort refresh-grants that session against the "+
				"IdP so a short-lived id_token can back a long web session. A missing cookie is "+
				"400; an expired or unknown one is 401.").
		Tag("Auth")
	apiroute.NoContent(e, http.MethodPost, "/auth/logout", h.Logout).
		Doc("Sign out",
			"Revokes the caller's session and expires the `refresh_token` cookie with the same "+
				"attributes it was set with (so browsers actually evict it). Local provider: "+
				"deletes the access + refresh token pair named by the Authorization header and "+
				"the cookie, and answers 403 when they do not both match. OIDC provider: deletes "+
				"the server-side oidc_sessions row AND every bearer that session minted, so they "+
				"die with it rather than up to 30 minutes later. Also clears the in-flight OIDC "+
				"state/nonce cookies so an abandoned link flow cannot be completed by the next "+
				"user of a shared browser. 204 on success.").
		Tag("Auth")
	// OIDC (Authentik) web login (boom-0oe.11) + account linking (boom-b5n.4).
	// login/link 404 unless OIDC is configured.
	//
	// All three are Redirect: every success path is a 302 c.Redirect with the
	// target in Location, so there is no JSON response body to document.
	apiroute.Redirect(e, http.MethodGet, "/auth/login/oidc", http.StatusFound, h.LoginOIDC).
		Doc("Start OIDC sign-in",
			"Browser entry point for the Authentik authorization-code flow. Mints a random "+
				"state + nonce, stores each in a 10-minute HttpOnly SameSite=Lax cookie, and "+
				"302s to the provider's authorize endpoint. Not an XHR endpoint — navigate to "+
				"it. Answers 404 'OIDC login is not enabled' when no OIDC resolver is "+
				"configured, which is why registering it unconditionally is safe.").
		Tag("Auth")
	apiroute.Redirect(e, http.MethodGet, "/auth/link/oidc", http.StatusFound, h.LinkOIDC).
		Doc("Start OIDC account linking",
			"Begins an authorization-code flow whose callback BINDS the resolved external "+
				"identity to the already-signed-in boomtime user instead of minting a new "+
				"session. Available whenever OIDC is CONFIGURED, including under "+
				"BOOM_AUTH_PROVIDER=local, so an existing password account can be linked before "+
				"flipping the provider over. The current user is resolved from the session "+
				"cookie because this is a top-level navigation that cannot carry an "+
				"Authorization header; an unauthenticated caller is 302'd to /login. 404 when "+
				"OIDC is not configured. The link intent is one-shot and expires in 10 minutes.").
		Tag("Auth")
	apiroute.Redirect(e, http.MethodGet, "/auth/callback/oidc", http.StatusFound, h.CallbackOIDC).
		Doc("OIDC redirect target",
			"Consumes ?code and ?state from the provider. Verifies state against the cookie set "+
				"at authorize time and the nonce against the id_token, then clears both flow "+
				"cookies (one-shot). LINK mode (state started by /auth/link/oidc) attaches the "+
				"identity and 302s to /app/settings?tab=profile&link=success|conflict|error, "+
				"leaving the existing session untouched. LOGIN mode provisions or resolves the "+
				"user, writes the opaque server-side session id into the `refresh_token` cookie "+
				"(HttpOnly, SameSite=Strict, expiring with the id_token) and 302s to /app. Every "+
				"failure is a 302 to /login?error=<reason> — state_mismatch, provider_error, "+
				"missing_code, login_failed — never a raw error page. 404 when OIDC is not "+
				"enabled.").
		Tag("Auth")
	apiroute.GET(e, "/api/v1/users/current/identities", h.ListIdentities).
		Doc("Linked external identities",
			"Everything the Settings > Account card needs in one read: the caller's linked "+
				"identities (provider, email, a 12-character prefix of the opaque subject for "+
				"display only, and the RFC3339 link timestamp), whether an OIDC resolver exists "+
				"at all so the card can offer 'Link Authentik' under the local provider, and "+
				"whether the account still has a usable password — which drives the 'you cannot "+
				"unlink your only sign-in method' copy.").
		Tag("Auth")
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/identities/:provider", h.UnlinkIdentity).
		Doc("Unlink an external identity",
			"Removes the caller's link for the named provider. Refuses with 409 when it would "+
				"be the last remaining sign-in method (no usable password AND this is the only "+
				"link), so a user can never lock themselves out. 404 when no link exists for "+
				"that provider; 204 on success.").
		Tag("Auth")
	// POSTNoBody: the body is OPTIONAL and decode errors are deliberately
	// ignored (see CreateAPIToken), which the seam's binder would turn into
	// a hard 400.
	apiroute.POSTNoBody(e, "/auth/create_api_token", h.CreateAPIToken).
		Doc("Mint a never-expiring API token",
			"Creates a personal API token for the caller and returns its raw value ONCE — it is "+
				"never retrievable again and is never written to a log. The request body is "+
				"OPTIONAL: when present it may carry {\"name\": \"...\"}, trimmed and truncated "+
				"to 42 characters, used as the token's label in the tokens list. A missing, "+
				"empty, or undecodable body is tolerated rather than rejected, so this route "+
				"binds inside the handler rather than through the seam.").
		Tag("Auth")
	apiroute.GET(e, "/auth/tokens", h.ListAPITokens).
		Doc("List API tokens",
			"Metadata for every API token the caller owns — id, label, and timestamps. The "+
				"token VALUES are not stored in retrievable form and are never returned; "+
				"/auth/create_api_token is the only place a raw token is ever emitted.").
		Tag("Auth")
	apiroute.NoContent(e, http.MethodDelete, "/auth/token/:id", h.DeleteToken).
		Doc("Revoke an API token",
			"Deletes the token with the given id, scoped to the caller. Deliberately answers "+
				"204 whether or not a row matched, so the endpoint is no oracle for probing "+
				"other users' token ids.").
		Tag("Auth")
	apiroute.NoContentBody[model.TokenMetadata](e, http.MethodPost, "/auth/token", h.UpdateToken).
		Doc("Rename an API token",
			"Updates the human-readable label of one of the caller's tokens. Body is capped at "+
				"4 KiB — it is a short name string. 204 on success.").
		Tag("Auth")
	apiroute.GET(e, "/auth/users/current", h.CurrentUser).
		Doc("Who am I (cookie-authed)",
			"Wakatime-compatible {data:{...}} identity envelope, resolved from the "+
				"`refresh_token` cookie rather than a bearer token. Carries the username, a "+
				"synthesized email, the admin flag from the BOOM_ADMIN_USERS allowlist, plus "+
				"boom-dg7's `timezone` (the raw stored value, empty when unset) and "+
				"`effective_timezone` (after the user > BOOM_DEFAULT_TIMEZONE > UTC resolution "+
				"chain). Editor plugins ignore the extra fields. A timezone lookup failure is "+
				"logged and degrades to an empty raw value rather than failing the whole "+
				"response — this endpoint is the plugin session check and must not flap.").
		Tag("Auth")

	// Change password (boom-6jm): auth'd, re-verifies the current password,
	// re-hashes with argon2id, and revokes every refresh token for the
	// owner so other browsers get bounced. Registered under the
	// users/current tree (not /auth/) so it uses the same access-token
	// auth as sibling /api/v1/users/current/* endpoints.
	apiroute.NoContentBody[changePasswordRequest](e, http.MethodPost, "/api/v1/users/current/password", h.ChangePassword).
		Doc("Change password",
			"Re-verifies {currentPassword} against the stored argon2id hash (401 when wrong — "+
				"distinct from the 403 that means 'your access token is bad'), enforces the "+
				"shared password policy on {newPassword} (400 with a user-safe reason), then in "+
				"ONE transaction rewrites the users row, deletes every refresh token for the "+
				"owner, and deletes every other 30-minute access token — preserving "+
				"never-expiring API tokens and the caller's own bearer, so the caller keeps "+
				"their session while every other browser is bounced. Body cap 4 KiB (two short "+
				"strings; a fat body would only amplify the argon2 verify). 204 on success.").
		Tag("Auth")

	// Public profile (boom-6jm.1): auth'd GET/PUT for the caller's own
	// enable-toggle + slug. The PUBLIC read endpoint (see below) resolves
	// the slug and returns the scrubbed payload.
	apiroute.GET(e, "/api/v1/users/current/profile", h.GetPublicProfile).
		Doc("Own public-profile settings",
			"The caller's opt-in public profile state: the enable toggle, the slug (null when "+
				"never set), and the social-card knobs cardTheme ('' | 'dark' | 'light') and "+
				"cardTagline. This is the editor-side read; the visitor-facing payload lives at "+
				"GET /api/public/profile/{slug}.")
	apiroute.PUT(e, "/api/v1/users/current/profile", h.PutPublicProfile).
		Doc("Update own public-profile settings",
			"Saves the enable toggle, slug, and social-card knobs, then returns the PERSISTED "+
				"shape so the client can settle without a follow-up GET. A slug is required when "+
				"enabling. Slugs are lowercased and trimmed, must match 3-30 lowercase "+
				"alphanumerics and hyphens with no leading or trailing hyphen, and may not be "+
				"one of the reserved names (admin, api, app, auth, login, register, settings, "+
				"p) — violations are 400, a slug already taken by another user is 409. "+
				"cardTheme accepts only '', 'dark' or 'light'; cardTagline is capped at 120 "+
				"runes. Both card fields are OPTIONAL POINTERS: omitting one leaves the stored "+
				"value untouched, so a toggle-only PUT cannot clobber a tagline. On success the "+
				"cached OpenGraph PNG for this user is deleted so the next unfurl re-renders "+
				"rather than waiting out the S3 TTL. Body cap 4 KiB.")

	// Encrypted-at-rest imported Wakatime API key (boom-6jm.2). GET
	// reports only {"hasSavedKey": bool} — plaintext is never returned.
	// POST persists a user-supplied key under AES-256-GCM. DELETE clears it.
	apiroute.GET(e, "/api/v1/users/current/wakatime_key", h.GetWakatimeKey).
		Doc("Saved Wakatime key status",
			"Reports ONLY whether the caller has an encrypted wakatime.com API key on file, "+
				"plus the last-known validity ('valid' | 'invalid' | 'unknown', omitted when "+
				"nothing is saved) and the RFC3339 timestamp of that check. The plaintext key — "+
				"and any prefix, suffix, or length of it — is deliberately never returned, "+
				"because the key is a bare UUID and even a few characters would narrow a brute "+
				"force meaningfully.").
		Tag("Integrations")
	apiroute.NoContentBody[wakatimeKeySaveRequest](e, http.MethodPost, "/api/v1/users/current/wakatime_key", h.SaveWakatimeKey).
		Doc("Save the Wakatime key",
			"Validate-then-persist: the server probes wakatime.com /users/current with the "+
				"supplied {key} BEFORE writing anything. A conclusive rejection (401/403 "+
				"upstream) returns 400 and stores nothing, so 'saved' always means 'validated'. "+
				"A network error, timeout, or upstream 5xx is NOT fatal — the key is stored with "+
				"status 'unknown' and the UI renders that neutrally. On success the plaintext is "+
				"encrypted with AES-256-GCM under BOOM_ENCRYPTION_KEY and overwrites any prior "+
				"key. A blank key is 400 rather than a silent clobber — use DELETE to clear. The "+
				"key is never logged. Body cap 4 KiB; 204 on success.").
		Tag("Integrations")
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/wakatime_key", h.DeleteWakatimeKey).
		Doc("Clear the saved Wakatime key",
			"Removes the caller's encrypted key and its status metadata. Idempotent — 204 "+
				"whether or not one existed, so clients need not GET first.").
		Tag("Integrations")

	// Per-user GitHub OAuth-App connection (boom-2ip Phase 1). The
	// status/disconnect API is ALWAYS registered — GET reports
	// {connected:false} and DELETE is a no-op when nothing is stored, and
	// the token is NEVER returned. The two /auth/github/* browser-redirect
	// routes register ONLY when the feature is fully configured
	// (Cfg.GithubConnectEnabled(): gate on + client id/secret + state signing
	// key), so with the default-off gate they simply 404 — inert.
	apiroute.GET(e, "/api/v1/users/current/github", h.GetGithubConnection).
		Doc("GitHub connection status",
			"Whether the caller has connected a GitHub OAuth App token, and if so the captured "+
				"GitHub login, the last-known token status, and when it was checked (RFC3339). "+
				"Always registered — it reports {connected:false} with the other fields omitted "+
				"when the feature is off or nothing is stored, so the Settings card degrades to "+
				"inert rather than 404ing. The access token itself is never returned.").
		Tag("Integrations")
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/users/current/github", h.DisconnectGithub).
		Doc("Disconnect GitHub",
			"Clears the caller's stored GitHub token and its metadata, and ALSO drops the "+
				"cached GitHub stats row so a stale cache cannot outlive the token that produced "+
				"it (and a re-connect starts clean). Idempotent — 204 whether or not a token "+
				"existed.").
		Tag("Integrations")
	if h != nil && h.Cfg != nil && h.Cfg.GithubConnectEnabled() {
		// Both are 302 c.Redirect on every success path — no JSON body to type.
		// Registered ONLY when GithubConnectEnabled(); otherwise these paths 404.
		apiroute.Redirect(e, http.MethodGet, "/auth/github/connect", http.StatusFound, h.ConnectGithub).
			Doc("Start the GitHub OAuth connect",
				"Browser entry point (window.location, not XHR) for connecting a GitHub OAuth "+
					"App token to the caller's account. The owner is resolved from the session "+
					"COOKIE because a top-level navigation cannot carry an Authorization header; "+
					"an unauthenticated caller is 302'd to /login. Mints an HMAC-signed `state` "+
					"that binds the round trip to that owner — the callback trusts the signature, "+
					"never a query parameter — and 302s to GitHub's authorize endpoint. "+
					"Registered only when BOOM_FEATURE_GITHUB_CONNECT is on AND the client id, "+
					"secret, and state signing key are all configured; otherwise the path 404s.").
			Tag("Integrations")
		apiroute.Redirect(e, http.MethodGet, "/auth/github/callback", http.StatusFound, h.CallbackGithub).
			Doc("GitHub OAuth redirect target",
				"Consumes ?code and ?state from GitHub. Verifies the signed state (owner, "+
					"signature, and a 10-minute freshness window), exchanges the code for a "+
					"token, validates it to capture the login, then encrypts and stores it. "+
					"ALWAYS a 302 back to /app/settings?tab=profile&github=<status>, where status "+
					"is connected, denied, state, missing_code, exchange, or error — the browser "+
					"never sees a raw error, and neither the code nor the token is ever logged.").
			Tag("Integrations")
		// boom-anh Phase 2: GitHub stats (authed cache-or-sync + public
		// cache-only). Gated with the connect routes — inert on a default boot.
		apiroute.GET(e, "/api/v1/users/current/github/stats", h.GetGithubStats).
			Doc("Own GitHub stats (cache-or-sync)",
				"Public GitHub aggregates for the caller — login, totals, the contribution grid, "+
					"top repositories, and language breakdown — plus fetchedAt and a `stale` "+
					"flag. Serves the cached row while it is under an hour old; when stale or "+
					"absent it synchronously re-syncs from GitHub, which UPSERTS exactly one row "+
					"per user (a re-sync replaces, never accumulates). Degradation is deliberate: "+
					"on a GitHub rate limit or a rejected token it serves the last-good cache "+
					"with stale=true and the X-Boom-Stats-Stale: true header rather than "+
					"failing, and only errors (503 rate-limited, 502 token rejected) when there "+
					"is no cache to fall back on. 404 when GitHub is not connected. The stored "+
					"token is never returned.").
			Tag("Integrations")
		// The public one writes its own body (hand-rolled ETag + 304
		// short-circuit), so it uses WritesJSON — the payload TYPE is still
		// declared, the handler still owns the bytes.
		apiroute.WritesJSON[model.GithubStatsPayload](e, http.MethodGet, "/api/public/profile/:slug/github/stats", h.PublicGithubStats).
			Doc("Public GitHub stats (no auth)",
				"The same GitHub aggregate payload as the authed endpoint, resolved through a "+
					"public profile slug and requiring no authentication. STRICTLY READ-ONLY: it "+
					"never triggers a sync, because a public hit must not burn the owner's GitHub "+
					"rate budget — an empty cache is a 404 rather than a fetch, and the owner (or "+
					"the backfill command) must populate it first. A malformed slug, an unknown "+
					"slug, and a profile with the public toggle off all return the same terse 404 "+
					"so slug existence is never confirmed. Sends Cache-Control: public, "+
					"max-age=60, must-revalidate plus a content-hash ETag, and answers a matching "+
					"If-None-Match with a bodyless 304.").
			Tag("Public Profile")
	}

	// catalyst-books HTTP surface (Amazon/Kindle/Audible + Hardcover + reading
	// items/work/curation/match) is registered separately by internal/books.Register
	// on the shared books.Handler (boom-zp2s Phase 2). It's mounted by the composition
	// root; book paths never overlap the identity paths, so relative order is immaterial.

	// Durable notifications (migration 00079) live on identity.Handler (domain-agnostic),
	// but stay gated on BooksEnabled() to preserve the pre-extraction registration exactly.
	// TODO(boom-zp2s): un-gate from BooksEnabled once notifications has its own gate.
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		apiroute.GET(e, "/api/v1/notifications", h.ListNotifications).
			Doc("Recent durable notifications",
				"The caller's stored notifications, newest first, plus the current unread count "+
					"— fetched on mount to seed the notification panel so an event that fired "+
					"while the user was offline (a finished book, a completed render) is replayed "+
					"rather than lost. ?limit defaults to 50 and accepts any positive integer; a "+
					"non-numeric or non-positive value falls back to the default rather than "+
					"erroring. `notifications` is null (not []) when there are none. Registered "+
					"only while the books feature gate is on.").
			Tag("Notifications")
		apiroute.POSTNoBody(e, "/api/v1/notifications/read", h.MarkNotificationsRead).
			Doc("Mark all notifications read",
				"Flips every unread notification for the caller to read and returns {marked}, "+
					"the number of rows that actually changed. Takes no request body and is "+
					"idempotent — a second call returns marked=0. Registered only while the books "+
					"feature gate is on.").
			Tag("Notifications")
	}

	// User IANA timezone (boom-dg7). GET reports the raw stored value
	// (''=unset) alongside the server's 3-level-resolved effectiveTimezone
	// so the FE can render "your choice" vs "server default" and only
	// auto-detect-and-prompt when the two differ. PATCH validates via
	// time.LoadLocation and rebuilds hb_rollup_daily so the dashboard
	// fast path immediately serves user-local buckets.
	apiroute.GET(e, "/api/v1/users/current/timezone", h.GetTimezone).
		Doc("Own timezone (raw + effective)",
			"Returns BOTH `timezone`, the raw stored IANA name with empty meaning 'never "+
				"picked', and `effectiveTimezone`, what the server actually buckets by after the "+
				"user > BOOM_DEFAULT_TIMEZONE > UTC resolution chain. The two are reported "+
				"separately so the Settings picker can say 'Using X (your choice)' versus 'Using "+
				"X (server default)' and only offer auto-detect when the browser's zone differs "+
				"from the effective one.")
	apiroute.PATCH(e, "/api/v1/users/current/timezone", h.UpdateTimezone).
		Doc("Set own timezone",
			"Accepts {\"timezone\": \"America/Los_Angeles\"}, validated with Go's "+
				"time.LoadLocation — an unknown name is 400. Surrounding whitespace is trimmed. "+
				"A blank or missing value CLEARS the explicit pick, which is the 'revert to "+
				"server default' affordance. Two side effects follow a successful write: "+
				"hb_rollup_daily is rebuilt for the owner so the dashboard fast path immediately "+
				"serves user-local day buckets (best-effort — a failure is logged and the next "+
				"ingest catches up, it does not fail the PATCH), and the owner's cached "+
				"aggregation payloads are invalidated so the next load is not served from the "+
				"pre-change TTL blob. Returns the same raw + effective pair as the GET. Body cap "+
				"4 KiB.")

	// ---- public profile + avatar (pre-refactor: registerMiscRoutes) ---
	// Public profile — resolves slug -> user, then renders a scrubbed
	// dashboard-shaped payload. UNAUTHENTICATED; the payload MUST go
	// through widget.Scrub before serialization. See profile.go.
	//
	// WritesJSON, not GET: it marshals publicProfileResponse itself to hash
	// the exact bytes into an ETag and answers If-None-Match with a bodyless
	// 304, so the seam (which owns the encode) can't express it — but the
	// payload TYPE is still declared here.
	apiroute.WritesJSON[publicProfileResponse](e, http.MethodGet, "/api/public/profile/:slug", h.PublicProfile).
		Doc("Public profile dashboard (no auth)",
			"Resolves a slug to its owner and returns a widget-scrubbed activity summary: "+
				"totals, the daily series, the project / language / editor / platform / category "+
				"segments, a punchcard, the owner's card tagline, and their saved public "+
				"dashboard layout when they have one. The Machines segment and every distinct-"+
				"count field are OMITTED on purpose — machine names identify a person in a way "+
				"project names do not, and counts would leak the size of hidden sets. A "+
				"malformed slug, an unknown slug, and a profile with the toggle off all return "+
				"the same terse 404, so the endpoint confirms nothing. The window is the "+
				"canonical 60 days but a visitor may rescope the STATS with ?days=N, clamped to "+
				"1-365; awards and streaks keep reading the canonical window so a rescoped view "+
				"never desyncs the ledger. Sends Cache-Control: public, max-age=60, "+
				"must-revalidate (short and revalidating on purpose, so disabling a profile "+
				"propagates promptly) with a content-hash ETag, and answers a matching "+
				"If-None-Match with a bodyless 304.").
		Tag("Public Profile")
	// gaka social-card: UNAUTHENTICATED OpenGraph image. Renders the owner's
	// "social-card" widget → SVG → 1200×630 PNG (resvg-go, CGO-free) as the
	// og:image an unfurl fetches. Raw image/png — c.Blob, never JSON.
	apiroute.Raw(e, http.MethodGet, "/api/public/profile/:slug/og.png", "image/png", http.StatusOK, h.PublicProfileOGImage).
		Doc("OpenGraph social card (PNG)",
			"The 1200x630 og:image a link unfurler fetches for /p/{slug}: the owner's "+
				"'social-card' widget rendered to SVG and rasterized to PNG. Unauthenticated, "+
				"and public data only — it goes through the same widget.Scrub path as the public "+
				"dashboard. An unknown or non-public slug gets a GENERIC boomtime-branded card "+
				"with a 200, NOT a 404, so an unfurl of a private or removed profile shows a "+
				"clean brand image instead of a broken one — and reveals nothing either way. "+
				"Real users are served through a durable S3 object cache when one is configured "+
				"(X-Card-Cache: hit|miss reports which); a cache read failure silently falls "+
				"back to a live render. Sends Cache-Control: public, max-age=600 with a "+
				"content-hash ETag and answers a matching If-None-Match with a bodyless 304. "+
				"Editing the card via PUT /api/v1/users/current/profile deletes the cached "+
				"object so edits do not wait out that window.").
		Tag("Public Profile")

	// boom-9v4: per-user CHIBI avatar. Prompt-synthesis SSE is authed
	// (currently admin-gated; see user_avatar.go for the rationale).
	// Regenerate + status are self-only (resolveUser gates on token).
	// Public GET serves the ready image bytes to the profile hero.
	//
	// synthesize-prompt proxies an SSE stream (Raw text/event-stream, written
	// chunk-by-chunk) and the public GET is a c.Blob (Raw image bytes) —
	// neither has a JSON response type. Regenerate is apiroute.Accepted (202,
	// no seam-side bind) because it binds under the MEDIUM 64 KiB body cap,
	// not the seam's SMALL one.
	apiroute.Raw(e, http.MethodPost, "/api/v1/admin/avatar/synthesize-prompt", "text/event-stream", http.StatusOK, h.SynthesizeAvatarPrompt).
		Doc("Author an avatar prompt (SSE stream)",
			"Takes {topLabels: [up to 3 short archetype names], synopsis: \"one-line activity "+
				"readout\"} and PROXIES an OpenAI-compatible streaming chat completion straight "+
				"back to the caller as text/event-stream — each `data:` line carries the "+
				"upstream's delta JSON, so an existing OpenAI stream parser works unchanged. The "+
				"response is NOT JSON and cannot be exercised from Swagger UI. The server owns "+
				"the system prompt (fixed chibi-portrait style guardrails) and the LLM API key, "+
				"which never reaches the browser. Admin-gated via BOOM_ADMIN_USERS because LLM "+
				"spend is per-token and unbounded — 401 without a token, 403 when not an admin. "+
				"503 when BOOM_LLM_API_KEY is unset, 502 when the upstream fails or returns "+
				"non-200 (its error body is logged, never forwarded). Upstream call is capped at "+
				"60 seconds; request body cap 4 KiB.").
		Tag("Avatar")
	apiroute.Accepted(e, http.MethodPost, "/api/v1/users/current/avatar/regenerate", h.RegenerateAvatar).
		Doc("Regenerate own avatar",
			"Reserves the caller's avatar row as 'running' and enqueues an owner-scoped "+
				"avatar-render job, then answers 202 with {\"status\":\"running\"} — the render "+
				"itself happens on a worker (falling back to an in-process goroutine when the "+
				"jobs subsystem is not wired) and completion arrives as a toast over "+
				"/api/v1/jobs/ws, or via polling GET /api/v1/users/current/avatar/status. The "+
				"row is reserved BEFORE returning so a poll immediately after the 202 can never "+
				"observe the previous 'ready'. Body is {prompt (required), model?, size?, seed?} "+
				"bound INSIDE the handler under the 64 KiB medium cap — a hand-tuned prompt is "+
				"far larger than the seam's 4 KiB default. A concurrent render for the same user "+
				"is refused with 409 rather than racing; 400 on an empty prompt; 503 when "+
				"BOOM_FEATURE_LABEL_IMAGES / BOOM_COMFYUI_SHIM_URL are not configured.").
		Tag("Avatar")
	apiroute.GET(e, "/api/v1/users/current/avatar/status", h.GetAvatarStatus).
		Doc("Own avatar render status",
			"Cheap tri-state poll — no image bytes, safe to call every few seconds during a "+
				"render. Returns {status} plus updatedAt, and error / generatedAt when they "+
				"apply. status is 'none' when the caller has never rendered one, which the UI "+
				"renders as an empty state distinct from a reserved-but-not-started "+
				"'running'; the terminal values are 'ready' and 'error'.").
		Tag("Avatar")
	apiroute.Raw(e, http.MethodGet, "/api/v1/users/:username/avatar", "image/png", http.StatusOK, h.UserAvatar).
		Doc("Avatar image bytes (no auth)",
			"Serves a user's rendered avatar as raw image bytes for the profile hero's <img>. "+
				"The Content-Type echoes the MIME captured at render time — image/png for "+
				"everything the bundled shim produces, with image/jpeg and image/webp possible. "+
				"404s unless the row is in the terminal 'ready' state with non-empty bytes, so "+
				"an in-flight render never leaks stale bytes to a fresh viewer, and 400 on an "+
				"empty username. Cache-Control is a deliberately modest public, max-age=30 "+
				"rather than immutable, because users iterate on their avatar during onboarding "+
				"and new bytes should propagate quickly; callers holding a generatedAt can "+
				"cache-bust harder with ?v=.").
		Tag("Avatar")

	// Per-user catalyst-go-jobs push stream (boom-hney.6): terminal job events
	// (e.g. avatar-render complete) for toasts. Cookie-authed in-handler.
	// WebSocket upgrade — 101, never a JSON response.
	apiroute.WebSocket(e, "/api/v1/jobs/ws", h.JobEventsWS).
		Doc("Job events stream (WebSocket)",
			"Upgrades to a WebSocket carrying the caller's TERMINAL catalyst-go-jobs events "+
				"(done / failed) as JSON messages — the app opens it once and toasts on each, "+
				"e.g. when an avatar render finishes. Authenticated from the `refresh_token` "+
				"COOKIE because a browser WebSocket handshake cannot set an Authorization "+
				"header; an invalid or expired cookie is answered with a 401 JSON envelope "+
				"instead of an upgrade. Strictly user-scoped through the hub — a subscriber only "+
				"ever receives their own events. When the jobs event hub is not wired the socket "+
				"is accepted and then closed normally with 'job events unavailable' so the "+
				"client backs off rather than reconnecting in a loop. Server-to-client only; "+
				"reads exist purely to detect disconnect.").
		Tag("Jobs")

	// Per-user domain-agnostic notification push stream: self-describing Events
	// (Type/Title/Body) for toasts. Cookie-authed in-handler.
	// WebSocket upgrade — 101, never a JSON response.
	apiroute.WebSocket(e, "/api/v1/notify/ws", h.NotifyWS).
		Doc("Notification stream (WebSocket)",
			"Upgrades to a WebSocket carrying the caller's domain-agnostic notification events "+
				"as JSON messages — each is self-describing (type, title, body) so the client "+
				"can toast it without knowing which subsystem produced it. Authenticated from "+
				"the `refresh_token` COOKIE because a browser WebSocket handshake cannot set an "+
				"Authorization header; an invalid or expired cookie is answered with a 401 JSON "+
				"envelope instead of an upgrade. Strictly user-scoped through the hub. When the "+
				"notify hub is not wired the socket is accepted and then closed normally with "+
				"'notifications unavailable' so the client backs off. Server-to-client only; "+
				"reads exist purely to detect disconnect. The durable replay of the same events "+
				"is GET /api/v1/notifications.").
		Tag("Notifications")
}
