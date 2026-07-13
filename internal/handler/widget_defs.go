// widget_defs.go: named/saved custom widget compositions (gaka-3nu). Sibling
// to widgets.go (widget_links). Ownership is per-user; the public renderer
// resolves the def's owner and applies the owner's curation the same way the
// widget_link renderer does.
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widget"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// widgetDefBody is the request shape for create/update — inline spec so a
// client can round-trip a builder Def as JSON without re-encoding to base64.
type widgetDefBody struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

// widgetDefMax bounds spec size — a builder Def is a few hundred bytes; 32 KiB
// leaves generous headroom while stopping runaway payloads.
const widgetDefMax = 32 * 1024

// validateWidgetDefSpec decodes the JSON spec into a widget.Def and runs the
// same layout/panel whitelist the URL path uses (widget.ValidateDef). Keeps
// the persistence path from accepting anything the render path would reject.
func validateWidgetDefSpec(spec json.RawMessage) (widget.Def, error) {
	if len(spec) == 0 {
		return widget.Def{}, fmt.Errorf("spec is empty")
	}
	if len(spec) > widgetDefMax {
		return widget.Def{}, fmt.Errorf("spec exceeds %d bytes", widgetDefMax)
	}
	var d widget.Def
	if err := json.Unmarshal(spec, &d); err != nil {
		return widget.Def{}, fmt.Errorf("spec is not a widget.Def: %w", err)
	}
	if err := widget.ValidateDef(d); err != nil {
		return widget.Def{}, err
	}
	return d, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) — used to translate the widget_defs (username,
// name) primary-key conflict into a friendly 400 instead of a 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ListWidgetDefs: GET /api/v1/users/current/widget-defs (auth).
func (h *Handler) ListWidgetDefs(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	defs, err := h.DB.ListWidgetDefs(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "widget def list failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"defs": defs})
}

// CreateWidgetDef: POST /api/v1/users/current/widget-defs (auth).
func (h *Handler) CreateWidgetDef(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var body widgetDefBody
	if err := c.Bind(&body); err != nil {
		return respondErr(c, apierr.BadRequest("Invalid JSON body"))
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return respondErr(c, apierr.BadRequest("name is required"))
	}
	if _, err := validateWidgetDefSpec(body.Spec); err != nil {
		return respondErr(c, apierr.BadRequest("Invalid spec: "+err.Error()))
	}
	id, err := h.DB.CreateWidgetDef(c.Request().Context(), owner, body.Name, body.Spec)
	if err != nil {
		// Unique-violation (username, name): report as 409 so the FE can
		// prompt the user to rename or PATCH the existing def.
		if isUniqueViolation(err) {
			return respondErr(c, apierr.BadRequest("A widget with this name already exists"))
		}
		return h.internalErr(c, "widget def create failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"defId": id.String(),
		"url":   h.Cfg.BadgeURL + "/widget/svg/" + id.String() + "/named",
	})
}

// UpdateWidgetDef: PATCH /api/v1/users/current/widget-defs/:name (auth). Same
// (owner, name) key as create — the URL name identifies the row to replace.
func (h *Handler) UpdateWidgetDef(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	name := c.Param("name")
	if name == "" {
		return respondErr(c, apierr.BadRequest("name is required"))
	}
	var body widgetDefBody
	if err := c.Bind(&body); err != nil {
		return respondErr(c, apierr.BadRequest("Invalid JSON body"))
	}
	if _, err := validateWidgetDefSpec(body.Spec); err != nil {
		return respondErr(c, apierr.BadRequest("Invalid spec: "+err.Error()))
	}
	ok, err := h.DB.UpdateWidgetDef(c.Request().Context(), owner, name, body.Spec)
	if err != nil {
		return h.internalErr(c, "widget def update failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("Widget def not found"))
	}
	// Cached SVG bytes for this def are keyed by def id — updating the spec
	// doesn't invalidate them automatically. Sweep the owner's cache so a
	// freshly-edited widget renders on the next fetch.
	h.invalidateOwnerCache(owner)
	return c.NoContent(http.StatusNoContent)
}

// DeleteWidgetDef: DELETE /api/v1/users/current/widget-defs/:name (auth).
func (h *Handler) DeleteWidgetDef(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	name := c.Param("name")
	if name == "" {
		return respondErr(c, apierr.BadRequest("name is required"))
	}
	ok, err := h.DB.DeleteWidgetDef(c.Request().Context(), owner, name)
	if err != nil {
		return h.internalErr(c, "widget def delete failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("Widget def not found"))
	}
	h.invalidateOwnerCache(owner)
	return c.NoContent(http.StatusNoContent)
}

// WidgetDefSvg: GET /widget/svg/:uuid/named (PUBLIC). Resolves the def uuid to
// its owner + spec, applies the owner's curation, and renders the composition
// with query params (days, theme, title). v1 is user-scoped only — no project
// or space slicing on named defs.
func (h *Handler) WidgetDefSvg(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return respondErr(c, apierr.BadRequest("Invalid widget def id"))
	}
	ctx := c.Request().Context()

	owner, saved, ok, err := h.DB.GetWidgetDef(ctx, id)
	if err != nil {
		return h.internalErr(c, "widget def lookup failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("Widget def not found"))
	}
	// Re-validate on read: the spec is trusted-at-write, but a schema change
	// could invalidate an older row. Fail loudly rather than silently render
	// with a stale layout enum.
	def, err := validateWidgetDefSpec(saved.Spec)
	if err != nil {
		return h.internalErr(c, "widget def spec no longer valid", err)
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

	// Same cache headers as WidgetSvg: cheap to re-render, still worth
	// asking camo/CDNs to hold on to the bytes.
	c.Response().Header().Set("Cache-Control", "public, max-age=300, s-maxage=300")

	key := cacheKey(owner, "widget-def", id.String(), days, theme, title, saved.UpdatedAt.Unix())
	return h.cachedBlob(c, key, "image/svg+xml", func() ([]byte, error) {
		t1 := time.Now().UTC()
		t0 := removeDays(t1, int(days))

		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		renames, err := h.DB.LoadRenameSets(ctx, owner)
		if err != nil {
			return nil, err
		}

		// v1: user scope. Reuse the same rollup-vs-raw gate as WidgetSvg.
		var rows []db.StatRow
		var members db.MemberSets
		if !hidden.HasHiddenOutside(db.RollupAxes) {
			rows, err = h.DB.GetUserActivityRollup(ctx, owner, t0, t1, hidden, renames, members, false)
		} else {
			rows, err = h.DB.GetUserActivity(ctx, owner, t0, t1, widgetTimeLimit, hidden, renames, members, false)
		}
		if err != nil {
			return nil, err
		}

		payload := stats.ToStatsPayload(t0, t1, rows, nil)
		data := &widget.Data{Payload: &payload}
		needs := widget.NeedsForDef(def)
		if needs.Grade {
			g := stats.Grade(&payload)
			data.Grade = &g
		}
		if needs.Punchcard {
			cells, err := h.DB.GetPunchcard(ctx, owner, t0, t1, widgetTimeLimit, hidden, members, false)
			if err != nil {
				return nil, err
			}
			pc := stats.ToPunchcardPayload(cells)
			data.Punchcard = &pc
		}
		if needs.Momentum {
			mrows, err := h.DB.GetMomentum(ctx, owner, t0, t1, widgetTimeLimit, hidden, renames, members, false)
			if err != nil {
				return nil, err
			}
			mp := stats.ToMomentumPayload(t0, t1, mrows, 6)
			data.Momentum = &mp
		}
		if needs.Sessions {
			srows, err := h.DB.GetSessions(ctx, owner, t0, t1, widgetTimeLimit, hidden, members, false)
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
		return widget.RenderCustom(data, def, opts)
	})
}
