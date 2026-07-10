package handler

import (
	"io"
	"net/http"
	"net/url"

	"github.com/TheBranchDriftCatalyst/gakatime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/model"
	"github.com/TheBranchDriftCatalyst/gakatime/internal/stats"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// BadgeLink: GET /badge/link/:project (auth) -> {"badgeUrl": "<HAKA_BADGE_URL>/badge/svg/<uuid>"}.
func (h *Handler) BadgeLink(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	project := c.Param("project")
	id, err := h.DB.CreateBadgeLink(c.Request().Context(), owner, project)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.JSON(http.StatusOK, model.BadgeResponse{
		BadgeURL: h.Cfg.BadgeURL + "/badge/svg/" + id.String(),
	})
}

// BadgeSvg: GET /badge/svg/:uuid?days (public) -> proxied SVG from shields.io.
func (h *Handler) BadgeSvg(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("svg"))
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	ctx := c.Request().Context()

	user, project, err := h.DB.GetBadgeLinkInfo(ctx, id)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}

	days := queryInt64(c, "days", 7)
	total, err := h.DB.GetTotalActivityTime(ctx, user, days, project)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}

	message := stats.CompoundDuration(&total)
	shieldURL := h.Cfg.ShieldsIOURL + "/static/v1?label=" + url.QueryEscape(project) +
		"&message=" + url.QueryEscape(message) + "&color=blue"

	resp, err := http.Get(shieldURL)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return respondErr(c, apierr.Generic())
	}
	return c.Blob(http.StatusOK, "image/svg+xml", body)
}
