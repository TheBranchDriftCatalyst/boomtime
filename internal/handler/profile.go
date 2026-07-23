// profile.go — endpoints for the opt-in public read-only profile (gaka-6jm.1).
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
package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widget"
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

// publicProfilePayloadDays is the default window for the public dashboard.
// 60 days is a compromise between "enough to see patterns" and "not so wide
// that a scraper can rebuild a long history". Matches the widget default
// close enough that FE devs recognize the shape.
const publicProfilePayloadDays = 60

// publicProfileTimeLimit locks the aggregation to the app default (15-min
// gap). The public payload does not accept a timeLimit override — it would
// fragment the (currently uncached) response space and expose a knob a
// public dashboard doesn't need.
const publicProfileTimeLimit int64 = 15

// getProfileResponse is GET /api/v1/users/current/profile.
type getProfileResponse struct {
	Enabled bool    `json:"enabled"`
	Slug    *string `json:"slug"`
}

// putProfileRequest is PUT /api/v1/users/current/profile body.
type putProfileRequest struct {
	Enabled bool   `json:"enabled"`
	Slug    string `json:"slug"`
}

// publicProfileResponse is the shape returned by GET /api/public/profile/:slug.
// Deliberately a fresh struct (not model.StatsPayload) so we control exactly
// which fields land in the JSON: no machines, no counts leak, adds a
// username label. The dashboard is DERIVED from a scrubbed StatsPayload —
// see the field-by-field copy below.
type publicProfileResponse struct {
	Username     string                `json:"username"`
	StartDate    time.Time             `json:"startDate"`
	EndDate      time.Time             `json:"endDate"`
	TotalSeconds int64                 `json:"totalSeconds"`
	DailyAvg     float64               `json:"dailyAvg"`
	DailyTotal   []int64               `json:"dailyTotal"`
	Projects     []model.ResourceStats `json:"projects"`
	Languages    []model.ResourceStats `json:"languages"`
	Editors      []model.ResourceStats `json:"editors"`
	Platforms    []model.ResourceStats `json:"platforms"`
	Categories   []model.ResourceStats `json:"categories"`
	Punchcard    model.PunchcardPayload `json:"punchcard"`
}

// GetPublicProfile: GET /api/v1/users/current/profile (auth). Returns the
// caller's public-profile toggle + slug.
func (h *Handler) GetPublicProfile(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	enabled, slug, err := h.DB.GetPublicProfile(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "public profile lookup failed", err)
	}
	return c.JSON(http.StatusOK, getProfileResponse{Enabled: enabled, Slug: slug})
}

// PutPublicProfile: PUT /api/v1/users/current/profile (auth). Saves the
// caller's public-profile toggle + slug. When enabled=true the slug is
// required and validated. Returns 409 on slug conflict, 400 on format /
// reservation violation. On success, returns the persisted shape so the FE
// can settle its local state without a follow-up GET.
func (h *Handler) PutPublicProfile(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req putProfileRequest
	// gaka-bi2: 4 KiB cap — the body is a bool + a slug bounded by
	// publicProfileSlugRe (≤30 chars).
	if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))

	// When enabling, a valid slug is required. When disabling, either omit
	// the slug (leave DB as-is) or supply a valid one (write it too).
	if req.Enabled {
		if req.Slug == "" {
			return respondErr(c, apierr.BadRequest("slug is required when enabling the public profile"))
		}
	}
	if req.Slug != "" {
		if !publicProfileSlugRe.MatchString(req.Slug) {
			return respondErr(c, apierr.BadRequest("slug must be 3-30 characters, lowercase letters, digits, and hyphens (no leading/trailing hyphen)"))
		}
		if _, hit := reservedSlugs[req.Slug]; hit {
			return respondErr(c, apierr.BadRequest("that slug is reserved — please pick another"))
		}
	}

	if err := h.DB.SetPublicProfile(c.Request().Context(), owner, req.Enabled, req.Slug); err != nil {
		// Translate a unique-violation on public_slug into 409 Conflict —
		// the FE surfaces this as an inline field error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return respondErr(c, apierr.New(http.StatusConflict, "that slug is already taken", nil))
		}
		return h.internalErr(c, "public profile save failed", err)
	}
	// Read back the persisted shape (SetPublicProfile may have left slug
	// alone on the off-with-no-slug path, so read is the source of truth).
	enabled, slug, err := h.DB.GetPublicProfile(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "public profile readback failed", err)
	}
	h.Logger.Info("public profile updated", "user", owner, "enabled", enabled)
	return c.JSON(http.StatusOK, getProfileResponse{Enabled: enabled, Slug: slug})
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
		return respondErr(c, apierr.NotFound("This profile isn't public"))
	}
	ctx := c.Request().Context()

	username, err := h.DB.LookupUsernameBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return respondErr(c, apierr.NotFound("This profile isn't public"))
		}
		return h.internalErr(c, "public profile slug lookup failed", err)
	}
	enabled, _, err := h.DB.GetPublicProfile(ctx, username)
	if err != nil {
		return h.internalErr(c, "public profile enabled check failed", err)
	}
	if !enabled {
		return respondErr(c, apierr.NotFound("This profile isn't public"))
	}

	// Build the payload. Range mirrors widget defaults (60d, 15-min gap).
	t1 := time.Now().UTC()
	t0 := removeDays(t1, publicProfilePayloadDays)

	hidden, err := h.DB.LoadHiddenSets(ctx, username)
	if err != nil {
		return h.internalErr(c, "public profile hidden load failed", err)
	}
	renames, err := h.DB.LoadRenameSets(ctx, username)
	if err != nil {
		return h.internalErr(c, "public profile rename load failed", err)
	}

	// No Space scoping for public profile — it's an account-level view.
	// members/spaceRequested are the zero value.
	var members db.MemberSets
	var rows []db.StatRow
	// Same rollup gate as widgets/dashboard: fast path when every hide
	// falls on rollup axes.
	if !hidden.HasHiddenOutside(db.RollupAxes) {
		rows, err = h.DB.GetUserActivityRollup(ctx, username, t0, t1, hidden, renames, members, false)
	} else {
		rows, err = h.DB.GetUserActivity(ctx, username, t0, t1, publicProfileTimeLimit, hidden, renames, members, false)
	}
	if err != nil {
		return h.internalErr(c, "public profile activity query failed", err)
	}
	categories, err := h.DB.GetCategoryDaily(ctx, username, t0, t1, publicProfileTimeLimit, hidden, renames, members, false)
	if err != nil {
		return h.internalErr(c, "public profile category query failed", err)
	}
	payload := stats.ToStatsPayload(t0, t1, rows, categories)

	// gaka-6jm.1: enforce the public-safe contract before any field lands
	// on the wire. Scrub strips hidden values from the OtherMembers tail
	// that capWithOther collapses in application code (top-N is already
	// hide-excluded by the SQL predicates above).
	scrubbed := widget.Scrub(&payload, hidden)

	// Punchcard also uses hidden — even though its cells are (dow, hour)
	// buckets with no name, the DB query filters heartbeats by the hidden
	// axes at scan time.
	pcCells, err := h.DB.GetPunchcard(ctx, username, t0, t1, publicProfileTimeLimit, hidden, members, false)
	if err != nil {
		return h.internalErr(c, "public profile punchcard query failed", err)
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
	}
	// gaka-6jm.12: Cache leak fix.
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
		return h.internalErr(c, "public profile marshal failed", err)
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
