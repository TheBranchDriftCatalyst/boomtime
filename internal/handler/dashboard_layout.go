// dashboard_layout.go — CRUD for per-user dashboard layout JSON (gaka-keb).
//
// The layout is placement metadata for widget-catalog kinds. Storage is
// intentionally opaque JSONB — the handler validates only:
//
//   - :scope is one of the small known-scope allowlist. Passing an arbitrary
//     scope from the wire would let a user squat rows for scopes we haven't
//     wired yet.
//   - Body parses as JSON and is under the Small body cap (4 KiB) — layouts
//     are small (a dozen tiles at most).
//
// The widget-KIND ids inside the layout are NOT validated here. The FE renderer
// silently drops unknown kinds when rendering (defense in depth against a
// stale catalog on either side); server-side validation would require pulling
// the widget-kind whitelist into the handler and risks a spec drift the FE
// wouldn't notice.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/labstack/echo/v5"
)

// dashboardLayoutScopes is the allowlist of :scope values the endpoint will
// accept. Extend when adding a new dashboard target (e.g. "overview"). The map
// value is unused — set membership only.
var dashboardLayoutScopes = map[string]struct{}{
	"public_profile": {},
}

// GetDashboardLayout: GET /api/v1/users/current/dashboard/:scope (auth).
// Returns the caller's persisted layout for scope wrapped as
// {"layout": <persisted JSON>}, or 404 when unset so the FE knows to fall
// back to its default layout.
//
// The envelope shape lets us extend the response later (e.g., an
// `updatedAt` timestamp for optimistic-concurrency) without a breaking
// change on the wire.
func (h *Handler) GetDashboardLayout(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	scope := c.Param("scope")
	if _, ok := dashboardLayoutScopes[scope]; !ok {
		return respondErr(c, apierr.BadRequest("unknown dashboard scope"))
	}
	raw, found, err := h.DB.GetDashboardLayout(c.Request().Context(), owner, scope)
	if err != nil {
		return h.internalErr(c, "dashboard layout lookup failed", err)
	}
	if !found {
		return respondErr(c, apierr.NotFound("no layout saved"))
	}
	// Envelope manually so `layout` field stays byte-identical to what was
	// persisted (json.Marshal on a struct with a RawMessage does honor the
	// bytes verbatim).
	return c.JSON(http.StatusOK, map[string]json.RawMessage{"layout": raw})
}

// PutDashboardLayout: PUT /api/v1/users/current/dashboard/:scope (auth).
// Body is {"layout": <opaque JSON>}. Size-capped at 4 KiB (a layout with 20
// tiles is well under 2 KiB). Validates the body is well-formed JSON with a
// non-null `layout` key before touching the DB; the exact widget-kind ids
// are trusted (defense in depth lives in the FE renderer which drops
// unknown kinds).
//
// Response is the persisted envelope {"layout": ...} so the FE can settle
// its react-query cache with the same shape it will read on subsequent
// GETs. The layout bytes are preserved verbatim through Set/Get — see the
// gaka-25r round-trip regression test.
func (h *Handler) PutDashboardLayout(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	scope := c.Param("scope")
	if _, ok := dashboardLayoutScopes[scope]; !ok {
		return respondErr(c, apierr.BadRequest("unknown dashboard scope"))
	}

	// Small cap (4 KiB) — see BodyLimitSmall doc for the amp-attack framing.
	// Use a plain body read + json.Unmarshal so the inner `layout` field
	// reaches the DB verbatim (the anti-tautology round-trip test depends
	// on byte-preservation of that inner value).
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, BodyLimitSmall)
	var env struct {
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		// json.Decoder wraps http.MaxBytesReader's error inside its own
		// syntax/io error path; sniff for the too-large marker to return
		// 413 vs 400.
		var mbre *http.MaxBytesError
		if errors.As(err, &mbre) {
			return respondErr(c, apierr.New(http.StatusRequestEntityTooLarge, "payload too large", ptrStr("limit=4096")))
		}
		return respondErr(c, apierr.BadRequest("invalid JSON body"))
	}
	if len(env.Layout) == 0 || string(env.Layout) == "null" {
		return respondErr(c, apierr.BadRequest("missing 'layout' field"))
	}

	if err := h.DB.SetDashboardLayout(c.Request().Context(), owner, scope, env.Layout); err != nil {
		return h.internalErr(c, "dashboard layout save failed", err)
	}
	// Read back so we return the persisted envelope (byte-identical to the
	// input for JSONB; deterministic settle target for the FE).
	raw, _, err := h.DB.GetDashboardLayout(c.Request().Context(), owner, scope)
	if err != nil {
		return h.internalErr(c, "dashboard layout readback failed", err)
	}
	return c.JSON(http.StatusOK, map[string]json.RawMessage{"layout": raw})
}

// DeleteDashboardLayout: DELETE /api/v1/users/current/dashboard/:scope
// (auth). Drops the saved row so subsequent renders use the default layout.
// Idempotent — 204 whether or not a row existed.
func (h *Handler) DeleteDashboardLayout(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	scope := c.Param("scope")
	if _, ok := dashboardLayoutScopes[scope]; !ok {
		return respondErr(c, apierr.BadRequest("unknown dashboard scope"))
	}
	if err := h.DB.DeleteDashboardLayout(c.Request().Context(), owner, scope); err != nil {
		return h.internalErr(c, "dashboard layout delete failed", err)
	}
	return c.NoContent(http.StatusNoContent)
}
