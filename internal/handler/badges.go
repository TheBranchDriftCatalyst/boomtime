package handler

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/stats"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// BadgeLink: GET /badge/link/:project (auth) -> {"badgeUrl": "<BOOM_BADGE_URL>/badge/svg/<uuid>"}.
func (h *Handler) BadgeLink(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")
	id, err := h.DB.CreateBadgeLink(c.Request().Context(), owner, project)
	if err != nil {
		return h.internalErr(c, "badge link creation failed", err)
	}
	return c.JSON(http.StatusOK, model.BadgeResponse{
		BadgeURL: h.Cfg.BadgeURL + "/badge/svg/" + id.String(),
	})
}

// applyBadgeCuration is the badge-endpoint half of the public-safe contract
// (bd gaka-6jm.3). Badges are cardinality-1: a badge whose subject is a hidden
// project has no partially-scrubbed representation — the caller MUST 404
// instead of leaking the project name (which is echoed as the shields.io
// label) or its total time.
//
// Returns "hidden" when project is on the user's hide list; the caller then
// responds with a NotFound so an outsider cannot enumerate which curated
// project names correspond to which minted badge ids.
//
// Case-insensitive to match db.LoadHiddenSets's lowercased storage and
// exclusionPredicate's `lower(col) = ANY($n)` semantics.
// The hidden parameter is a model.HiddenSets so this helper is unit-testable
// without spinning up the DB — production wires db.HiddenSets (which satisfies
// the interface); tests wire model.HiddenSetsMap.
func applyBadgeCuration(hidden model.HiddenSets, project string) string {
	if hidden == nil {
		return project
	}
	needle := strings.ToLower(project)
	for _, hp := range hidden.Projects() {
		if hp == needle {
			return "hidden"
		}
	}
	return project
}

// BadgeSvg: GET /badge/svg/:uuid?days (public) -> proxied SVG from shields.io.
func (h *Handler) BadgeSvg(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("svg"))
	if err != nil {
		return respondErr(c, apierr.BadRequest("Invalid badge id"))
	}
	ctx := c.Request().Context()

	user, project, ok, err := h.DB.GetBadgeLinkInfo(ctx, id)
	if err != nil {
		return h.internalErr(c, "badge link lookup failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("Badge not found"))
	}

	// gaka-6jm.3: apply the owner's hide rules before hitting the DB for
	// activity totals. If the badge's subject project has been curated away,
	// the badge itself must 404 — otherwise the shields.io label leaks the
	// project name and the total leaks per-day activity.
	hidden, err := h.DB.LoadHiddenSets(ctx, user)
	if err != nil {
		return h.internalErr(c, "badge hidden sets load failed", err)
	}
	if applyBadgeCuration(hidden, project) == "hidden" {
		return respondErr(c, apierr.NotFound("Badge not found"))
	}

	days := queryInt64(c, "days", 7)
	total, err := h.DB.GetTotalActivityTime(ctx, user, days, project)
	if err != nil {
		return h.internalErr(c, "badge activity query failed", err)
	}

	message := stats.CompoundDuration(&total)
	shieldURL := h.Cfg.ShieldsIOURL + "/static/v1?label=" + url.QueryEscape(project) +
		"&message=" + url.QueryEscape(message) + "&color=blue"

	resp, err := httpClient.Get(shieldURL)
	if err != nil {
		h.Logger.Error("shields.io request failed", "err", err)
		return respondErr(c, apierr.New(http.StatusBadGateway, "Badge upstream request failed", nil))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.Logger.Warn("shields.io returned non-200", "status", resp.StatusCode)
		return respondErr(c, apierr.New(http.StatusBadGateway, "Badge upstream request failed", nil))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return h.internalErr(c, "badge upstream read failed", err)
	}
	return c.Blob(http.StatusOK, "image/svg+xml", body)
}
