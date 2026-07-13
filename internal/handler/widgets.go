// widgets.go: the embeddable-widget endpoints (gaka-hsj). Auth'd link CRUD
// (mint/list/delete) plus the PUBLIC SVG renderer. The public endpoint is the
// privacy-sensitive one: it must apply the owner's hide/rename curation so
// curated-away data never leaks into a README embed.
package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widget"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// widgetDaysDefault/Max bound the public endpoint's range: an embeds default of
// 30 days, hard-capped at 366 so a stray param can't force an all-time raw scan.
const (
	widgetDaysDefault = 30
	widgetDaysMax     = 366
)

// widgetTimeLimit is locked to the app default (15-min gap). Public widgets do
// not accept a timeLimit override — it would fragment the cache and expose a
// knob nobody needs on an embed.
const widgetTimeLimit int64 = 15

// WidgetLink: GET /api/v1/users/current/widgets/link?scopeType=&scopeRef= (auth).
// Upserts the (owner, scope) link after validating the requester owns the scope.
func (h *Handler) WidgetLink(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	ctx := c.Request().Context()
	scopeType := c.QueryParam("scopeType")
	scopeRef := c.QueryParam("scopeRef")

	switch scopeType {
	case db.WidgetScopeUser:
		scopeRef = "" // account-wide: ref is always empty
	case db.WidgetScopeProject:
		ok, err := h.DB.ProjectExists(ctx, owner, scopeRef)
		if err != nil {
			return h.internalErr(c, "widget link project check failed", err)
		}
		if !ok {
			// gaka-xuc: the FE gets remapped project names from ProjectList
			// (which applies loadRenames), but the raw projects table only
			// carries source names. Accept scopeRef when it is the target of
			// an exact rename rule — the widget renderer expands the
			// scope-ref back to the source project(s) at query time.
			rs, err := h.DB.LoadRenameSets(ctx, owner)
			if err != nil {
				return h.internalErr(c, "widget link rename load failed", err)
			}
			if len(rs.ExactSourcesFor("project", scopeRef)) == 0 {
				return respondErr(c, apierr.NotFound("Unknown project"))
			}
		}
	case db.WidgetScopeSpace:
		id, err := strconv.Atoi(scopeRef)
		if err != nil {
			return respondErr(c, apierr.BadRequest("Invalid space id"))
		}
		sp, _, err := h.DB.GetSpace(ctx, owner, id)
		if err != nil {
			return h.internalErr(c, "widget link space check failed", err)
		}
		if sp == nil {
			return respondErr(c, apierr.NotFound("Unknown space"))
		}
	default:
		return respondErr(c, apierr.BadRequest("scopeType must be user, project or space"))
	}

	id, err := h.DB.CreateWidgetLink(ctx, owner, scopeType, scopeRef)
	if err != nil {
		return h.internalErr(c, "widget link creation failed", err)
	}
	return c.JSON(http.StatusOK, model.WidgetLinkResponse{
		WidgetBaseURL: h.Cfg.BadgeURL + "/widget/svg/" + id.String(),
		LinkID:        id.String(),
	})
}

// WidgetLinkList: GET /api/v1/users/current/widgets/links (auth) — Settings UI.
func (h *Handler) WidgetLinkList(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	links, err := h.DB.ListWidgetLinks(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "widget link list failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"links": links})
}

// WidgetLinkRoll: POST /api/v1/users/current/widgets/link/:id/roll (auth).
// Mints a new uuid for the same (user, scope). Returns the new URL — old id
// immediately 404s (existing embeds break; the point is exactly to break
// them). Owner-scoped: cross-owner ids 404.
func (h *Handler) WidgetLinkRoll(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	oldID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return respondErr(c, apierr.BadRequest("Invalid widget link id"))
	}
	newID, ok, err := h.DB.RollWidgetLink(c.Request().Context(), owner, oldID)
	if err != nil {
		return h.internalErr(c, "widget link roll failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("Widget link not found"))
	}
	// Any previously-cached bytes lived under the old id in the cache key, so
	// they can't accidentally be served post-roll — but invalidate defensively
	// (cheap; owner-prefixed sweep).
	h.invalidateOwnerCache(owner)
	return c.JSON(http.StatusOK, model.WidgetLinkResponse{
		WidgetBaseURL: h.Cfg.BadgeURL + "/widget/svg/" + newID.String(),
		LinkID:        newID.String(),
	})
}

// WidgetSvg: GET /widget/svg/:uuid/:kind?days=30&theme=dark (PUBLIC).
// Resolves the uuid to its (owner, scope), applies the owner's curation
// (hide/rename), builds the same StatsPayload the dashboard uses, and renders
// a self-contained SVG. Responses are cached in-process (owner-prefixed key,
// so curation changes bust them) and marked cacheable for camo/CDNs.
func (h *Handler) WidgetSvg(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return respondErr(c, apierr.BadRequest("Invalid widget link id"))
	}
	kind := c.Param("kind")
	// gaka-567: "custom" is the builder-composed kind — spec is inline in the
	// URL as ?spec=<base64>. Every other kind is on the fixed whitelist.
	var customDef *widget.Def
	if widget.IsCustomKind(kind) {
		def, err := widget.DecodeDef(c.QueryParam("spec"))
		if err != nil {
			return respondErr(c, apierr.BadRequest("Invalid widget spec: "+err.Error()))
		}
		customDef = &def
	} else if !widget.IsKind(kind) {
		return respondErr(c, apierr.NotFound("Unknown widget kind"))
	}
	ctx := c.Request().Context()

	owner, scopeType, scopeRef, ok, err := h.DB.GetWidgetLinkInfo(ctx, id)
	if err != nil {
		return h.internalErr(c, "widget link lookup failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("Widget link not found"))
	}

	// Track the request so the Settings badge can show "last requested Nm ago"
	// and its click-through popover can list unique origins. Best-effort:
	// don't fail the render if the hit-record write hits an issue.
	if err := h.DB.RecordWidgetLinkHit(ctx, id, c.Request().Referer()); err != nil {
		h.Logger.Debug("record widget hit failed", "id", id, "err", err)
	}

	days := queryInt64(c, "days", widgetDaysDefault)
	if days < 1 {
		days = 1
	}
	if days > widgetDaysMax {
		days = widgetDaysMax
	}
	theme := c.QueryParam("theme")
	title := c.QueryParam("title")
	spec := c.QueryParam("spec") // empty for non-custom; part of the cache key

	// GitHub camo respects these; the in-process cache below absorbs repeats
	// that arrive within the TTL anyway.
	c.Response().Header().Set("Cache-Control", "public, max-age=300, s-maxage=300")

	key := cacheKey(owner, "widget", id.String(), kind, days, theme, title, spec)
	return h.cachedBlob(c, key, "image/svg+xml", func() ([]byte, error) {
		t1 := time.Now().UTC()
		t0 := removeDays(t1, int(days))

		// Privacy: ALWAYS apply the owner's curation to the public payload.
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		renames, err := h.DB.LoadRenameSets(ctx, owner)
		if err != nil {
			return nil, err
		}

		// Scope: project reuses the Space inclusion path via a synthesized
		// single-project member set; space loads its rules by id (ownership was
		// validated at mint time and spaces cannot change owner). For project
		// scopes the member set is EXPANDED via the rename map (gaka-xuc) so
		// scopeRef="B" (a rename target) also matches raw heartbeats stored
		// under the original name "A" that maps A -> B.
		var members db.MemberSets
		scoped := false
		switch scopeType {
		case db.WidgetScopeProject:
			members = db.ProjectMemberSetWithRenames(scopeRef, renames)
			scoped = true
		case db.WidgetScopeSpace:
			sid, err := strconv.Atoi(scopeRef)
			if err != nil {
				return nil, fmt.Errorf("corrupt space scope_ref %q", scopeRef)
			}
			if members, err = h.DB.LoadMemberSets(ctx, sid); err != nil {
				return nil, err
			}
			scoped = true
		}

		// Same rollup-vs-raw gate as the dashboard Stats handler.
		var rows []db.StatRow
		if !hidden.HasHiddenOutside(db.RollupAxes) && (!scoped || !members.HasMemberOutside(db.RollupAxes)) {
			rows, err = h.DB.GetUserActivityRollup(ctx, owner, t0, t1, hidden, renames, members, scoped)
		} else {
			rows, err = h.DB.GetUserActivity(ctx, owner, t0, t1, widgetTimeLimit, hidden, renames, members, scoped)
		}
		if err != nil {
			return nil, err
		}

		payload := stats.ToStatsPayload(t0, t1, rows, nil)
		data := &widget.Data{Payload: &payload}
		needs := widget.Needs(kind)
		if customDef != nil {
			needs = widget.NeedsForDef(*customDef)
		}
		if needs.Grade {
			g := stats.Grade(&payload)
			data.Grade = &g
		}
		if needs.Punchcard {
			cells, err := h.DB.GetPunchcard(ctx, owner, t0, t1, widgetTimeLimit, hidden, members, scoped)
			if err != nil {
				return nil, err
			}
			pc := stats.ToPunchcardPayload(cells)
			data.Punchcard = &pc
		}
		if needs.Momentum {
			mrows, err := h.DB.GetMomentum(ctx, owner, t0, t1, widgetTimeLimit, hidden, renames, members, scoped)
			if err != nil {
				return nil, err
			}
			mp := stats.ToMomentumPayload(t0, t1, mrows, 6)
			data.Momentum = &mp
		}
		if needs.Sessions {
			srows, err := h.DB.GetSessions(ctx, owner, t0, t1, widgetTimeLimit, hidden, members, scoped)
			if err != nil {
				return nil, err
			}
			sp := stats.ToSessionsPayload(t0, t1, srows)
			data.Sessions = &sp
		}
		opts := widget.Options{
			Theme:    theme,
			Title:    title,
			Subtitle: fmt.Sprintf("last %d days", days),
		}
		if customDef != nil {
			return widget.RenderCustom(data, *customDef, opts)
		}
		return widget.Render(kind, data, opts)
	})
}
