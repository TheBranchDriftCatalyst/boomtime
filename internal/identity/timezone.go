package identity

// timezone.go (gaka-dg7): per-user IANA timezone endpoints + shared resolver.
//
// The resolver (resolveUserTZ) is the ONLY place handlers should derive the
// effective TZ for a query. Every SQL that extracts dow/hour/date from
// time_sent now takes a $tz bind param computed by this function — the
// 3-level resolution chain (user > env default > UTC) is enforced in one
// place so we can't half-apply the fix as new endpoints get added.

import (
	"context"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// resolveUserTZ returns the effective IANA name for a user's dow/hour/date
// buckets. NEVER returns "" — safe to thread into an AT TIME ZONE bind
// param without further guarding. On a DB lookup failure we log and fall
// through to the operator default (or UTC) so a transient blip on the users
// row doesn't break every stats query for that request.
func (h *Handler) resolveUserTZ(ctx context.Context, owner string) string {
	userTZ, err := h.DB.GetUserTimezone(ctx, owner)
	if err != nil {
		h.Logger.Warn("resolveUserTZ: users.timezone lookup failed; falling back to defaults",
			"user", owner, "err", err)
		userTZ = ""
	}
	return db.ResolveTimezone(userTZ, h.Cfg.DefaultTimezone)
}

// timezoneUpdateRequest is the body of PATCH /api/v1/users/current/timezone.
// A blank or missing `timezone` clears the explicit pick (falls back to
// BOOM_DEFAULT_TIMEZONE / UTC in the resolver).
type timezoneUpdateRequest struct {
	Timezone string `json:"timezone"`
}

// timezoneGetResponse mirrors the extended /auth/users/current shape but is
// also returned by GET /api/v1/users/current/timezone so the Settings picker
// can round-trip through a dedicated endpoint. Includes both the raw stored
// value (empty=unset) AND what the server ACTUALLY resolves to via the
// 3-level chain so the FE can render "Using X (from server default)" vs
// "Using X (your choice)" and only auto-detect+prompt when the two differ.
type timezoneGetResponse struct {
	Timezone          string `json:"timezone"`          // raw stored (empty=unset)
	EffectiveTimezone string `json:"effectiveTimezone"` // resolved
}

// GetTimezone: GET /api/v1/users/current/timezone.
func (h *Handler) GetTimezone(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	raw, err := h.DB.GetUserTimezone(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "timezone lookup failed", err)
	}
	effective := db.ResolveTimezone(raw, h.Cfg.DefaultTimezone)
	return c.JSON(http.StatusOK, timezoneGetResponse{
		Timezone:          raw,
		EffectiveTimezone: effective,
	})
}

// UpdateTimezone: PATCH /api/v1/users/current/timezone.
//
// Body: {"timezone": "America/Los_Angeles"} — validated via
// time.LoadLocation (Go's IANA-name gate). A blank/missing value clears the
// explicit pick, which is the "revert to server default" affordance for the
// Settings picker.
func (h *Handler) UpdateTimezone(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req timezoneUpdateRequest
	// gaka-bi2: 4 KiB cap. IANA names top out at ~40 chars; a fat body here
	// is just a client bug or an attacker probing.
	if aerr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitSmall); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	// Trim whitespace on the way in — pasted values from the picker can pick
	// up trailing spaces that break LoadLocation with a "unknown time zone"
	// message that is genuinely the client's fault but is hard to debug.
	tz := trimTimezoneName(req.Timezone)
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return apihelpers.RespondErr(c, apierr.BadRequest("invalid IANA timezone name"))
		}
	}
	if err := h.DB.SetUserTimezone(c.Request().Context(), owner, tz); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "timezone update failed", err)
	}
	// Rebuilding hb_rollup_daily under the new TZ is required for the fast
	// path (get_user_activity_rollup.sql) to report user-local buckets — the
	// rollup table stores `day` as a real date computed in the sender's TZ
	// at ingest. Best-effort: log on failure but don't fail the PATCH,
	// because the next ingest batch will refresh whatever days it touches
	// anyway. See internal/db/ingest.go RefreshRollup for the tx-scoped
	// worker.
	if err := h.DB.RefreshRollup(c.Request().Context(), owner, time.Time{}); err != nil {
		h.Logger.Warn("post-timezone-change rollup rebuild failed; next ingest will catch up",
			"user", owner, "err", err)
	}
	// Invalidate the owner's cached aggregation payloads so the FE sees new
	// buckets on the next dashboard load instead of the pre-change TTL blob.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	h.Logger.Info("user timezone updated", "user", owner, "timezone", tz)
	effective := db.ResolveTimezone(tz, h.Cfg.DefaultTimezone)
	return c.JSON(http.StatusOK, timezoneGetResponse{
		Timezone:          tz,
		EffectiveTimezone: effective,
	})
}

// trimTimezoneName strips ASCII whitespace only; IANA names never contain
// whitespace, so a caller intending to send "America/Los_Angeles" but with
// a stray leading/trailing space has done nothing else wrong — accept it.
func trimTimezoneName(s string) string {
	// stdlib's strings.TrimSpace handles the common cases (space, tab,
	// newline). IANA names are pure ASCII so no need for a Unicode dance.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			// skip surrounding whitespace only — not internal, which
			// would silently mutate a valid name. Two-pass:
			if len(out) == 0 {
				continue
			}
			// Interior whitespace — treat as invalid: the input
			// should have been trimmed by the client. Leave it in
			// so LoadLocation rejects with a clear error.
			out = append(out, b)
			continue
		}
		out = append(out, b)
	}
	// Trailing whitespace: pop off any run of whitespace at the tail.
	for len(out) > 0 {
		last := out[len(out)-1]
		if last == ' ' || last == '\t' || last == '\n' || last == '\r' {
			out = out[:len(out)-1]
			continue
		}
		break
	}
	return string(out)
}
