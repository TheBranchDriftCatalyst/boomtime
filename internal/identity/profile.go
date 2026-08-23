// profile.go — endpoints for the opt-in public read-only profile (boom-6jm.1).
//
// SECURITY POSTURE:
//
//   - The public endpoint (GET /api/public/profile/:slug) is UNAUTHENTICATED.
//     It is the ONE non-widget route that leaks per-user aggregates, so every
//     payload MUST be routed through widget.Scrub before serialization. The
//     scrubber contract is documented at length in internal/widget/scrub.go —
//     re-read that file before changing anything in Handler.PublicProfile.
//
//   - The DB queries already exclude hidden values from the top-N segments
//     (LoadHiddenSets threads into the aggregation predicates). Scrub is the
//     belt to the SQL braces: it walks the OtherMembers tail collapsed by
//     capWithOther in application code, which the SQL predicates don't reach.
//
//   - We explicitly OMIT the Machines segment from the public JSON. The
//     scrubber leaves it (it's a curated axis), but machine names ("djs-mbp")
//     are identifying in a way project/language names aren't. Cheap privacy.
//
//   - Slug regex + reserved-name blocklist enforced BEFORE the DB write.
//     Format is intentionally narrow (lowercase, digits, hyphens; 3-30 chars)
//     so slugs stay URL-safe, human-readable, and can never look like a
//     reserved route path.
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/widget"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// publicProfileSlugRe: lowercase alphanumeric + hyphens, 3-30 chars, no
// leading/trailing hyphen. Anchored to avoid partial matches. The 30-char
// upper bound keeps public URLs short; the 3-char lower bound keeps
// single-letter slugs from monopolizing high-value real estate ("a", "b")
// and matches the FE Zod schema.
var publicProfileSlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,28}[a-z0-9])?$`)

// reservedSlugs: slugs that would collide with app routes or be confusingly
// named. Rejected with 400. Kept intentionally small — the URL prefix `/p/`
// isolates the public route so the SPA won't try to render these paths.
var reservedSlugs = map[string]struct{}{
	"admin":    {},
	"api":      {},
	"app":      {},
	"auth":     {},
	"login":    {},
	"register": {},
	"settings": {},
	"p":        {},
}

// publicProfilePayloadDays / publicProfileTimeLimit are the canonical
// exported constants — see handler.go's PublicProfilePayloadDays and
// PublicProfileTimeLimit. Local unexported aliases kept so the rest of
// this file reads as it did pre-collapse (60 / 15). Awards imports the
// exported form (identity.PublicProfilePayloadDays).
const (
	publicProfilePayloadDays       = PublicProfilePayloadDays
	publicProfileTimeLimit   int64 = PublicProfileTimeLimit
)

// getProfileResponse is GET /api/v1/users/current/profile. CardTheme /
// CardTagline carry the owner's social-card customization (gaka social-card)
// so the profile editor's Social Card section round-trips them.
type getProfileResponse struct {
	Enabled     bool    `json:"enabled"`
	Slug        *string `json:"slug"`
	CardTheme   string  `json:"cardTheme"`
	CardTagline string  `json:"cardTagline"`
}

// putProfileRequest is PUT /api/v1/users/current/profile body. CardTheme /
// CardTagline are the optional social-card knobs; omitted (nil) leaves the
// stored value untouched so a toggle-only PUT doesn't clobber the tagline.
type putProfileRequest struct {
	Enabled     bool    `json:"enabled"`
	Slug        string  `json:"slug"`
	CardTheme   *string `json:"cardTheme,omitempty"`
	CardTagline *string `json:"cardTagline,omitempty"`
}

// cardTaglineMaxLen bounds the persisted tagline — it feeds og:description
// (Discord/Twitter truncate ~200 chars anyway) and the card hero line.
const cardTaglineMaxLen = 120

// validCardThemes is the allow-list for the social-card theme knob. Mirrors
// internal/widget/themes.go's registered theme names; "" means "renderer
// default" (dark/synthwave).
var validCardThemes = map[string]struct{}{"": {}, "dark": {}, "light": {}}

// publicProfileResponse is the shape returned by GET /api/public/profile/:slug.
// Deliberately a fresh struct (not model.StatsPayload) so we control exactly
// which fields land in the JSON: no machines, no counts leak, adds a
// username label. The dashboard is DERIVED from a scrubbed StatsPayload —
// see the field-by-field copy below.
//
// boom-keb: the optional `Layout` field carries the owner's persisted
// dashboard-layout JSON when present. Absent when the owner has never
// customized their layout — the FE falls back to a default. Keeping this on
// the same payload means the public dashboard is still a single fetch (no
// separate public-layout endpoint that could get out of sync with the
// activity payload's cache lifetime).
type publicProfileResponse struct {
	Username     string                 `json:"username"`
	StartDate    time.Time              `json:"startDate"`
	EndDate      time.Time              `json:"endDate"`
	TotalSeconds int64                  `json:"totalSeconds"`
	DailyAvg     float64                `json:"dailyAvg"`
	DailyTotal   []int64                `json:"dailyTotal"`
	Projects     []model.ResourceStats  `json:"projects"`
	Languages    []model.ResourceStats  `json:"languages"`
	Editors      []model.ResourceStats  `json:"editors"`
	Platforms    []model.ResourceStats  `json:"platforms"`
	Categories   []model.ResourceStats  `json:"categories"`
	Punchcard    model.PunchcardPayload `json:"punchcard"`
	// gaka social-card: the owner's optional card tagline (feeds the FE
	// social-card hero preview + the OG description). Empty when unset.
	Tagline string          `json:"tagline,omitempty"`
	Layout  json.RawMessage `json:"layout,omitempty"`
}

// GetPublicProfile: GET /api/v1/users/current/profile (auth). Returns the
// caller's public-profile toggle + slug.
func (h *Handler) GetPublicProfile(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	enabled, slug, err := h.DB.GetPublicProfile(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile lookup failed", err)
	}
	theme, tagline, err := h.DB.GetPublicProfileCard(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile card lookup failed", err)
	}
	return c.JSON(http.StatusOK, getProfileResponse{
		Enabled: enabled, Slug: slug, CardTheme: theme, CardTagline: tagline,
	})
}

// PutPublicProfile: PUT /api/v1/users/current/profile (auth). Saves the
// caller's public-profile toggle + slug. When enabled=true the slug is
// required and validated. Returns 409 on slug conflict, 400 on format /
// reservation violation. On success, returns the persisted shape so the FE
// can settle its local state without a follow-up GET.
func (h *Handler) PutPublicProfile(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req putProfileRequest
	// boom-bi2: 4 KiB cap — the body is a bool + a slug bounded by
	// publicProfileSlugRe (≤30 chars).
	if aerr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitSmall); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))

	// When enabling, a valid slug is required. When disabling, either omit
	// the slug (leave DB as-is) or supply a valid one (write it too).
	if req.Enabled {
		if req.Slug == "" {
			return apihelpers.RespondErr(c, apierr.BadRequest("slug is required when enabling the public profile"))
		}
	}
	if req.Slug != "" {
		if !publicProfileSlugRe.MatchString(req.Slug) {
			return apihelpers.RespondErr(c, apierr.BadRequest("slug must be 3-30 characters, lowercase letters, digits, and hyphens (no leading/trailing hyphen)"))
		}
		if _, hit := reservedSlugs[req.Slug]; hit {
			return apihelpers.RespondErr(c, apierr.BadRequest("that slug is reserved — please pick another"))
		}
	}

	// Social-card knobs (gaka social-card) — validated before any DB write so a
	// bad theme/tagline is a clean 400, not a partial save.
	if req.CardTheme != nil {
		if _, ok := validCardThemes[strings.TrimSpace(*req.CardTheme)]; !ok {
			return apihelpers.RespondErr(c, apierr.BadRequest("cardTheme must be 'dark', 'light', or empty"))
		}
	}
	if req.CardTagline != nil && len([]rune(*req.CardTagline)) > cardTaglineMaxLen {
		return apihelpers.RespondErr(c, apierr.BadRequest(fmt.Sprintf("cardTagline must be at most %d characters", cardTaglineMaxLen)))
	}

	if err := h.DB.SetPublicProfile(c.Request().Context(), owner, req.Enabled, req.Slug); err != nil {
		// Translate a unique-violation on public_slug into 409 Conflict —
		// the FE surfaces this as an inline field error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return apihelpers.RespondErr(c, apierr.New(http.StatusConflict, "that slug is already taken", nil))
		}
		return apihelpers.InternalErr(h.Logger, c, "public profile save failed", err)
	}

	// Persist the card knobs only when the request carried them (nil = leave
	// as-is), reading current values first so a partial PUT preserves the
	// other field.
	if req.CardTheme != nil || req.CardTagline != nil {
		curTheme, curTagline, err := h.DB.GetPublicProfileCard(c.Request().Context(), owner)
		if err != nil {
			return apihelpers.InternalErr(h.Logger, c, "public profile card readback failed", err)
		}
		if req.CardTheme != nil {
			curTheme = strings.TrimSpace(*req.CardTheme)
		}
		if req.CardTagline != nil {
			curTagline = strings.TrimSpace(*req.CardTagline)
		}
		if err := h.DB.SetPublicProfileCard(c.Request().Context(), owner, curTheme, curTagline); err != nil {
			return apihelpers.InternalErr(h.Logger, c, "public profile card save failed", err)
		}
	}

	// Read back the persisted shape (SetPublicProfile may have left slug
	// alone on the off-with-no-slug path, so read is the source of truth).
	enabled, slug, err := h.DB.GetPublicProfile(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile readback failed", err)
	}
	theme, tagline, err := h.DB.GetPublicProfileCard(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile card readback failed", err)
	}
	// The card image may have changed (theme/tagline/enable/slug) — drop the
	// cached PNG so the next unfurl re-renders instead of waiting out the S3
	// TTL (boom-fym5). Best-effort: a stale/missing object next request is
	// harmless, so a Delete failure never fails the save.
	if h.Cards != nil {
		if derr := h.Cards.Delete(c.Request().Context(), owner); derr != nil {
			h.Logger.Warn("social-card cache invalidation failed", "user", owner, "err", derr)
		}
	}

	h.Logger.Info("public profile updated", "user", owner, "enabled", enabled)
	return c.JSON(http.StatusOK, getProfileResponse{
		Enabled: enabled, Slug: slug, CardTheme: theme, CardTagline: tagline,
	})
}

// PublicProfile: GET /api/public/profile/:slug (NO auth). Resolves slug ->
// username. If the user has enabled=false or the slug is unknown, returns
// 404 with an intentionally-terse message ("not public") so slug existence
// isn't confirmed to random walkers.
//
// Builds a StatsPayload for the last publicProfilePayloadDays and passes
// it through widget.Scrub before ANY field is copied into the response.
// See internal/widget/scrub.go for the public-safe contract. This handler
// is the second public-facing consumer of that scrubber (after the widget
// SVG endpoint); adding a third: reuse widget.Scrub, don't reimplement.
func (h *Handler) PublicProfile(c *echo.Context) error {
	slug := strings.ToLower(strings.TrimSpace(c.Param("slug")))
	// Cheap format guard: an ill-formed slug can't exist in the DB — skip
	// the query and 404 immediately (also stops the DB from getting
	// scrapper-hammered on garbage input).
	if slug == "" || !publicProfileSlugRe.MatchString(slug) {
		return apihelpers.RespondErr(c, apierr.NotFound("This profile isn't public"))
	}
	ctx := c.Request().Context()

	username, err := h.DB.LookupUsernameBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apihelpers.RespondErr(c, apierr.NotFound("This profile isn't public"))
		}
		return apihelpers.InternalErr(h.Logger, c, "public profile slug lookup failed", err)
	}
	enabled, _, err := h.DB.GetPublicProfile(ctx, username)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile enabled check failed", err)
	}
	if !enabled {
		return apihelpers.RespondErr(c, apierr.NotFound("This profile isn't public"))
	}

	// Build the payload. Range defaults to the canonical window (60d, 15-min
	// gap) but a visitor may re-scope the STATS via ?days=N (boom-174.7).
	//
	// This rescopes ONLY the dashboard stats. Labels/awards are computed by a
	// SEPARATE endpoint (/api/public/profile/:slug/awards) that keeps reading
	// the canonical publicProfilePayloadDays constant — so a re-scoped view
	// never desyncs the award ledger / streaks (the boom-hc6.3 invariant is on
	// the canonical computation, which is unchanged here).
	days := publicProfilePayloadDays
	if q := c.QueryParam("days"); q != "" {
		if n, perr := strconv.Atoi(q); perr == nil {
			// Clamp to a sane window: at least a day, at most a year.
			if n < 1 {
				n = 1
			} else if n > 365 {
				n = 365
			}
			days = n
		}
	}
	t1 := time.Now().UTC()
	t0 := apihelpers.RemoveDays(t1, days)

	scrubbed, hidden, tz, members, err := h.loadPublicActivity(ctx, username, t0, t1)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile activity load failed", err)
	}

	// Punchcard also uses hidden — even though its cells are (dow, hour)
	// buckets with no name, the DB query filters heartbeats by the hidden
	// axes at scan time.
	pcCells, err := h.DB.GetPunchcard(ctx, username, t0, t1, publicProfileTimeLimit, tz, hidden, members, false)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile punchcard query failed", err)
	}

	// boom-keb: read the owner's persisted layout for the public_profile
	// scope. Errors here are logged and swallowed — the FE has a default
	// layout to fall back to, and a broken layout row shouldn't 500 a
	// public read that would otherwise succeed.
	layoutRaw, hasLayout, err := h.DB.GetDashboardLayout(ctx, username, "public_profile")
	if err != nil {
		h.Logger.Warn("public profile layout lookup failed", "user", username, "err", err)
	}

	// gaka social-card: the owner's card tagline feeds the FE social-card hero
	// preview. Public data (only ever shown on the already-public card); a
	// lookup hiccup just omits it — never 500s an otherwise-good read.
	_, tagline, err := h.DB.GetPublicProfileCard(ctx, username)
	if err != nil {
		h.Logger.Warn("public profile card lookup failed", "user", username, "err", err)
	}

	// Deliberate copy — omits Machines entirely, no *Count fields (those
	// would leak a distinct-count for hidden values on axes whose top-N
	// list happens to be short).
	resp := publicProfileResponse{
		Username:     username,
		StartDate:    scrubbed.StartDate,
		EndDate:      scrubbed.EndDate,
		TotalSeconds: scrubbed.TotalSeconds,
		DailyAvg:     scrubbed.DailyAvg,
		DailyTotal:   scrubbed.DailyTotal,
		Projects:     scrubbed.Projects,
		Languages:    scrubbed.Languages,
		Editors:      scrubbed.Editors,
		Platforms:    scrubbed.Platforms,
		Categories:   scrubbed.Categories,
		Punchcard:    stats.ToPunchcardPayload(pcCells),
		Tagline:      tagline,
	}
	if hasLayout {
		resp.Layout = layoutRaw
	}
	// boom-6jm.12: Cache leak fix.
	//
	// Previously we sent `public, max-age=300, s-maxage=300`, which meant a
	// disabled profile could keep serving from a downstream cache (CDN, Camo,
	// browser) for up to 5 minutes after the user flipped the toggle off.
	//
	// The new policy trades some CDN efficiency for prompt privacy propagation:
	//   - max-age=60         — browser caches for 60s (not 5m)
	//   - must-revalidate    — after that window, clients MUST hit the origin
	//                          (no stale-while-revalidate serving)
	//   - s-maxage dropped   — shared caches follow max-age; we no longer
	//                          instruct CDNs to hold a longer copy
	//
	// The ETag lets the origin answer revalidation cheaply with a 304 for
	// unchanged payloads. Body hash (sha-256 truncated to 16 hex chars) keeps
	// it stable across identical payloads and cheap to compute.
	body, err := json.Marshal(resp)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "public profile marshal failed", err)
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	c.Response().Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	c.Response().Header().Set("ETag", etag)
	// If-None-Match short-circuit: matched ETag returns 304 with no body,
	// letting the client's cached copy stay valid for another max-age window.
	if match := c.Request().Header.Get("If-None-Match"); match != "" && match == etag {
		return c.NoContent(http.StatusNotModified)
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	c.Response().WriteHeader(http.StatusOK)
	_, werr := c.Response().Write(body)
	return werr
}

// ---- social card / OpenGraph (gaka social-card) -----------------------------

// socialCardW / socialCardH are the OpenGraph image dimensions — the canonical
// 1200×630 that Discord/Twitter/Slack/Facebook unfurl at. Matches the
// "social-card" widget spec's size in internal/widget/specs.json.
const (
	socialCardW = 1200
	socialCardH = 630
)

// loadPublicActivity builds the SCRUBBED public StatsPayload for username over
// [t0,t1], applying the owner's hide/rename curation exactly like the public
// dashboard does. It is the shared load path behind both PublicProfile (JSON)
// and the OG social card (PNG) — the public-safe contract (widget.Scrub) lives
// in ONE place. Returns the hidden set + resolved tz + (always-empty) member
// set so callers that need follow-on queries (punchcard) reuse the same
// curation without re-loading it.
func (h *Handler) loadPublicActivity(ctx context.Context, username string, t0, t1 time.Time) (*model.StatsPayload, db.HiddenSets, string, db.MemberSets, error) {
	var members db.MemberSets
	hidden, err := h.DB.LoadHiddenSets(ctx, username)
	if err != nil {
		return nil, hidden, "", members, err
	}
	renames, err := h.DB.LoadRenameSets(ctx, username)
	if err != nil {
		return nil, hidden, "", members, err
	}
	// boom-dg7: public profile shows the OWNER's data in the OWNER's timeframe.
	tz := apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, username, h.Cfg.DefaultTimezone)

	// No Space scoping for public profile — it's an account-level view.
	var rows []db.StatRow
	if !hidden.HasHiddenOutside(db.RollupAxes) {
		rows, err = h.DB.GetUserActivityRollup(ctx, username, t0, t1, hidden, renames, members, false)
	} else {
		rows, err = h.DB.GetUserActivity(ctx, username, t0, t1, publicProfileTimeLimit, tz, hidden, renames, members, false)
	}
	if err != nil {
		return nil, hidden, "", members, err
	}
	categories, err := h.DB.GetCategoryDaily(ctx, username, t0, t1, publicProfileTimeLimit, tz, hidden, renames, members, false)
	if err != nil {
		return nil, hidden, "", members, err
	}
	payload := stats.ToStatsPayload(t0, t1, rows, categories, nil)
	// boom-6jm.1: enforce the public-safe contract before any field leaves.
	scrubbed := widget.Scrub(&payload, hidden)
	return scrubbed, hidden, tz, members, nil
}

// resolvePublicSlug maps a URL slug to its owner username IFF the slug is
// well-formed AND its owner has the public profile ENABLED. Returns ("", false)
// for every negative case (bad slug, unknown slug, disabled profile, DB error)
// with no distinction between them — the same no-oracle policy the JSON
// endpoint uses ("This profile isn't public" for all).
func (h *Handler) resolvePublicSlug(ctx context.Context, slug string) (string, bool) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" || !publicProfileSlugRe.MatchString(slug) {
		return "", false
	}
	username, err := h.DB.LookupUsernameBySlug(ctx, slug)
	if err != nil {
		return "", false
	}
	enabled, _, err := h.DB.GetPublicProfile(ctx, username)
	if err != nil || !enabled {
		return "", false
	}
	return username, true
}

// buildSocialCardData assembles the widget.Data for the "social-card" spec: the
// scrubbed public payload over the canonical window + a computed grade + the
// owner's public identity (username + tagline). Also returns the owner's chosen
// card theme ("" = renderer default). Uses publicProfilePayloadDays (NOT a
// visitor ?days=) so the shared OG image is stable and cacheable.
func (h *Handler) buildSocialCardData(ctx context.Context, username string) (*widget.Data, string, error) {
	t1 := time.Now().UTC()
	t0 := apihelpers.RemoveDays(t1, publicProfilePayloadDays)
	scrubbed, _, _, _, err := h.loadPublicActivity(ctx, username, t0, t1)
	if err != nil {
		return nil, "", err
	}
	g := stats.Grade(scrubbed)
	theme, tagline, err := h.DB.GetPublicProfileCard(ctx, username)
	if err != nil {
		return nil, "", err
	}
	data := &widget.Data{
		Payload:  scrubbed,
		Grade:    &g,
		Identity: &widget.Identity{Username: username, Tagline: tagline},
	}
	return data, theme, nil
}

// PublicProfileOGImage: GET /api/public/profile/:slug/og.png (NO auth). Renders
// the owner's "social-card" widget → SVG → 1200×630 PNG (via widget.RenderPNG,
// resvg-go, CGO-free) as the og:image for unfurls. PUBLIC DATA ONLY: the same
// scrubbed payload path the public dashboard uses. A non-public / unknown slug
// gets a GENERIC boomtime-branded card (200, no user data) rather than a 404,
// so an unfurl of a private/removed profile shows a clean brand card instead of
// a broken image — and reveals nothing (no oracle).
func (h *Handler) PublicProfileOGImage(c *echo.Context) error {
	ctx := c.Request().Context()
	username, isUser := h.resolvePublicSlug(ctx, c.Param("slug"))

	// Real public users pass through the durable S3 cache (boom-fym5): one
	// object per user, refreshed ~daily off its LastModified. MinIO stays
	// private — the app is the only S3 client (passthrough, not a redirect) —
	// and the ETag/Cache-Control below still lets repeat crawler/browser hits
	// 304 without touching S3 or the renderer. The generic brand card (unknown
	// slug) is cheap + identical for everyone, so it's always rendered live.
	if isUser && h.Cards != nil {
		if cached, hit, gerr := h.Cards.Get(ctx, username); gerr == nil && hit && cached.Fresh {
			c.Response().Header().Set("X-Card-Cache", "hit")
			return serveCardPNG(c, cached.PNG)
		} else if gerr != nil {
			// A cache read error must not fail the unfurl — fall through to a
			// live render (and a fresh Put) below.
			h.Logger.Warn("social-card cache read failed; rendering live", "user", username, "err", gerr)
		}
		png, rerr := h.renderCardPNG(ctx, username, true)
		if rerr != nil {
			return apihelpers.InternalErr(h.Logger, c, "og social card render failed", rerr)
		}
		if perr := h.Cards.Put(ctx, username, png); perr != nil {
			h.Logger.Warn("social-card cache write failed", "user", username, "err", perr)
		}
		c.Response().Header().Set("X-Card-Cache", "miss")
		return serveCardPNG(c, png)
	}

	png, err := h.renderCardPNG(ctx, username, isUser)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "og social card render failed", err)
	}
	return serveCardPNG(c, png)
}

// renderCardPNG builds the 1200×630 card PNG for a real public user (isUser) or
// the generic boomtime brand card (!isUser). Extracted so the cache-fill path
// and the live-render path share one implementation (boom-fym5).
func (h *Handler) renderCardPNG(ctx context.Context, username string, isUser bool) ([]byte, error) {
	theme := "dark"
	var svg []byte
	var err error
	if !isUser {
		svg, err = widget.RenderBrandCard(theme)
	} else {
		data, cardTheme, derr := h.buildSocialCardData(ctx, username)
		if derr != nil {
			return nil, derr
		}
		if cardTheme != "" {
			theme = cardTheme
		}
		svg, err = widget.RenderSpec("social-card", data, widget.Options{
			Theme:    theme,
			Subtitle: fmt.Sprintf("last %d days", publicProfilePayloadDays),
		})
	}
	if err != nil {
		return nil, err
	}
	return widget.RenderPNG(ctx, svg, socialCardW, socialCardH)
}

// serveCardPNG writes the PNG with a content-hash ETag + a browser/CDN cache
// window, answering If-None-Match with 304. Shared by the cache-hit,
// cache-miss, and live-render paths so every response is byte-for-byte
// consistent (boom-fym5).
func serveCardPNG(c *echo.Context, png []byte) error {
	sum := sha256.Sum256(png)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	// A shareable image doesn't change often; cache 10m at the browser/CDN and
	// let the ETag answer revalidation cheaply. S3 holds the durable copy, and
	// owner curation invalidates it (Delete) so edits don't wait out the window.
	c.Response().Header().Set("Cache-Control", "public, max-age=600")
	c.Response().Header().Set("ETag", etag)
	if match := c.Request().Header.Get("If-None-Match"); match != "" && match == etag {
		return c.NoContent(http.StatusNotModified)
	}
	return c.Blob(http.StatusOK, "image/png", png)
}

// OGMeta is the OpenGraph/Twitter metadata the server injects into the SPA
// shell's <head> for a /p/:slug request (see server.registerStatic). Every
// field is already HTML/attribute-safe when built by BuildOGMeta — the caller
// still escapes on injection as defense-in-depth.
type OGMeta struct {
	Title       string // og:title / twitter:title
	Description string // og:description / twitter:description
	ImageURL    string // absolute og:image / twitter:image
	ProfileURL  string // canonical og:url
}

// BuildOGMeta resolves a slug to its public OG metadata for server-side <head>
// injection. Returns (nil, false) for a non-public / unknown slug so the caller
// serves the generic default block. baseURL is the ABSOLUTE public origin
// (scheme://host, no trailing slash) the caller resolved (cfg.BadgeURL or the
// request). The description is a stats headline ("357h coded · TypeScript 42% ·
// 30-day streak · grade A"), optionally led by the owner's tagline.
func (h *Handler) BuildOGMeta(ctx context.Context, slug, baseURL string) (*OGMeta, bool) {
	username, ok := h.resolvePublicSlug(ctx, slug)
	if !ok {
		return nil, false
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	t1 := time.Now().UTC()
	t0 := apihelpers.RemoveDays(t1, publicProfilePayloadDays)
	scrubbed, _, _, _, err := h.loadPublicActivity(ctx, username, t0, t1)
	if err != nil {
		// A DB hiccup shouldn't strip the unfurl entirely — fall back to a
		// minimal headline so og:image (which self-renders) still carries.
		h.Logger.Warn("og meta stats load failed", "user", username, "err", err)
		scrubbed = &model.StatsPayload{}
	}
	_, tagline, err := h.DB.GetPublicProfileCard(ctx, username)
	if err != nil {
		h.Logger.Warn("og meta card load failed", "user", username, "err", err)
	}

	baseURL = strings.TrimRight(baseURL, "/")
	return &OGMeta{
		Title:       "@" + username + " · boomtime",
		Description: buildStatsHeadline(scrubbed, tagline),
		ImageURL:    baseURL + "/api/public/profile/" + slug + "/og.png",
		ProfileURL:  baseURL + "/p/" + slug,
	}, true
}

// buildStatsHeadline composes the og:description one-liner from a scrubbed
// public payload, optionally led by the owner's tagline. Parts are joined with
// " · " and empty parts dropped so a fresh/empty profile still yields a clean
// sentence.
func buildStatsHeadline(p *model.StatsPayload, tagline string) string {
	var parts []string
	if t := strings.TrimSpace(tagline); t != "" {
		parts = append(parts, t)
	}
	if p.TotalSeconds > 0 {
		if d := stats.CompoundDuration(&p.TotalSeconds); d != "" {
			parts = append(parts, d+" coded")
		}
	}
	if len(p.Languages) > 0 {
		top := p.Languages[0]
		if top.OtherCount == 0 && top.Name != "" {
			parts = append(parts, fmt.Sprintf("%s %d%%", top.Name, int(top.TotalPct+0.5)))
		}
	}
	if s := stats.CurrentStreak(p.DailyTotal); s > 0 {
		parts = append(parts, fmt.Sprintf("%d-day streak", s))
	}
	if p.TotalSeconds > 0 {
		g := stats.Grade(p)
		parts = append(parts, "grade "+g.Level)
	}
	if len(parts) == 0 {
		return "Self-hosted coding activity on boomtime."
	}
	return strings.Join(parts, " · ")
}
