// Package identity owns the auth-facing HTTP + profile + timezone +
// wakatime_key + avatar endpoints. Extracted from the god-type
// handler.Handler as part of gaka-8tn phase 4a.
//
// SECURITY POSTURE: this package covers the auth surface (Login, Register,
// RefreshToken, Logout, ChangePassword, API-token CRUD) plus the
// user-scoped settings (public profile, timezone, wakatime key, avatar).
// The auth flows are load-bearing security invariants; the assertions in
// the *_test.go files here are byte-identical to their pre-refactor
// counterparts on purpose — do NOT simplify them.
//
// The public-facing endpoints (GET /api/public/profile/:slug, the CHIBI
// avatar public GET) live here too because their auth policy + read paths
// share the users-table dependency. Every payload that leaves via the
// public route MUST go through widget.Scrub before serialization — see
// internal/widget/scrub.go for the contract.
//
// Shared helpers (RespondErr / TokenFromHeader / ResolveUser /
// ResolveOwnerFromCookie / QueryInt64 / BindJSONWithLimit / InternalErr
// / CachedJSON / CachedBlob / InvalidateOwnerCache) live in
// internal/apihelpers/ — this package imports that instead of carrying
// per-file shims (gaka-8tn phase 8 collapse).
package identity

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cardstore"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/github"
	"github.com/labstack/echo/v5"
)

// Handler bundles the SUBSET of the god-type handler.Handler's
// dependencies that the identity domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every users / auth_tokens / refresh_tokens read + write,
//     plus timezone / wakatime_key / public_profile / user_avatars
//   - Cfg    — cookie Secure/APIPrefix, argon2 policy, session expiry,
//     LLM/ComfyUI feature flags, DefaultTimezone, IsAdmin allowlist
//   - Logger — password-change / wakatime-save / avatar-regen log lines
//   - Cache  — invalidated on timezone change so dashboards re-bucket
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Cache  *cache.TTL
	// GithubStats is the GitHub stats refresh service (gaka-anh Phase 2). Used
	// by the authed /github/stats endpoint's on-demand-if-stale path. Always
	// non-nil in production (constructed in New with the real GitHub
	// endpoints); it is inert until a GET hits a stale/absent cache. Handler-
	// level tests overwrite this field with github.NewServiceForTest pointed at
	// a mock-GitHub httptest server.
	GithubStats *github.Service
	// Cards is the durable S3/MinIO social-card cache (gaka-fym5). nil when
	// BOOM_S3_* is unset (or the client failed to init) — the og.png handler
	// then renders live on every request rather than erroring.
	Cards *cardstore.Store
}

// New constructs an identity.Handler with the passed-in shared deps.
// Every field is required in production; nil-checks are the caller's
// responsibility (the god-type's New wires all four unconditionally).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	// Durable social-card cache (gaka-fym5). Unset config → (nil, nil); a
	// client-init failure → logged + nil. Either way Cards stays nil and the
	// og.png handler renders live, so the feature degrades gracefully.
	cards, err := cardstore.New(cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL)
	if err != nil {
		logger.Warn("social-card S3 cache disabled: client init failed", "err", err)
		cards = nil
	}
	return &Handler{
		DB:          database,
		Cfg:         cfg,
		Logger:      logger,
		Cache:       cch,
		GithubStats: github.NewService(database, logger),
		Cards:       cards,
	}
}

// PublicProfilePayloadDays is the default window for the public dashboard
// payload rendered by GET /api/public/profile/:slug. Exported so awards
// (which computes the SAME payload window when evaluating
// /public/profile/:slug/awards streaks) reads ONE canonical value — a
// drift here vs. there would show up as a label present on /p/:slug but
// missing on the awards mirror (gaka-hc6.3 invariant).
const PublicProfilePayloadDays = 60

// PublicProfileTimeLimit locks the aggregation to the app default (15-min
// gap). Exported so awards reads the SAME cap. The public payload does
// not accept a timeLimit override — it would fragment the (currently
// uncached) response space and expose a knob a public dashboard doesn't
// need.
const PublicProfileTimeLimit int64 = 15

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *admin.Handler / *curation.Handler — kept here because
// user_avatar.go's SynthesizeAvatarPrompt gates on it. Three byte-
// identical copies survive because each domain guards a distinct
// endpoint and a shared helper would need dependency-injection scaffolding
// bigger than the 8-line body itself.
//
// The 403 path deliberately does NOT distinguish "unknown admin config"
// from "not on the list" — both look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}

// httpClient is the shared outbound HTTP client for the identity
// package: the wakatime.com probe (wakatime_key.go SaveWakatimeKey).
// A dedicated timeout avoids http.DefaultClient's unbounded default
// which would hang a save if wakatime.com locks up.
//
// EXPOSED as a package-level var so auth_seams_test.go can swap it out
// via SwapHTTPClientForTest. Not exported directly — tests use the
// SwapHTTPClientForTest seam to keep the mutation site auditable.
var httpClient = &http.Client{Timeout: 15 * time.Second}
