package widgets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// BadgeLink: GET /badge/link/:project (auth) -> {"badgeUrl": "<BOOM_BADGE_URL>/badge/svg/<uuid>"}.
func (h *Handler) BadgeLink(c *echo.Context) (model.BadgeResponse, error) {
	var out model.BadgeResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	project := c.Param("project")
	id, err := h.DB.CreateBadgeLink(c.Request().Context(), owner, project)
	if err != nil {
		return out, fmt.Errorf("badge link creation failed: %w", err)
	}
	return model.BadgeResponse{
		BadgeURL: h.Cfg.BadgeURL + "/badge/svg/" + id.String(),
	}, nil
}

// applyBadgeCuration is the badge-endpoint half of the public-safe contract
// (bd boom-6jm.3). Badges are cardinality-1: a badge whose subject is a hidden
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

// badgeTotalSeconds sums the badge subject's tracked seconds over the window.
//
// boom-l827: the raw query matches `project = $3` against heartbeats, but a
// badge is minted from the name the FE DISPLAYS — and once a rename rule
// merges "myrepo-v2" into "MyRepo", "MyRepo" is the only name any surface
// shows while every stored heartbeat still says "myrepo-v2". The badge then
// rendered "MyRepo | 0" forever next to a widget link showing real hours,
// because widget links got the rename expansion in boom-xuc
// (db.ProjectMemberSetWithRenames) and badges did not. So: add the subject's
// own total to the total of every raw project an EXACT rename rule maps onto
// it. Regex/template renames are deliberately not expanded — same contract as
// db.RenameSets.ExactSourcesFor, which cannot enumerate a pattern's inputs.
//
// Curation still wins: a source project the owner has hidden contributes
// nothing, so a rename can never smuggle a hidden project's time back into a
// public badge (boom-6jm.3).
//
// Known limitation, inherited from ExactSourcesFor + the exact-match SQL: the
// returned source names are lowercased, and get_total_project_time.sql compares
// `project = $3` case-sensitively, so a raw project stored with capitals is
// only counted when the badge's own name matches it exactly. Making that
// case-insensitive means changing the shared query, which is out of scope here.
func (h *Handler) badgeTotalSeconds(ctx context.Context, owner, project string, days int64, hidden model.HiddenSets) (int64, error) {
	total, err := h.DB.GetTotalActivityTime(ctx, owner, days, project)
	if err != nil {
		return 0, err
	}
	renames, err := h.DB.LoadRenameSets(ctx, owner)
	if err != nil {
		return 0, err
	}
	for _, src := range renames.ExactSourcesFor("project", project) {
		if strings.EqualFold(src, project) {
			continue // the subject itself, already counted
		}
		if applyBadgeCuration(hidden, src) == "hidden" {
			continue // a hidden source stays hidden, even behind a rename
		}
		sub, err := h.DB.GetTotalActivityTime(ctx, owner, days, src)
		if err != nil {
			return 0, err
		}
		total += sub
	}
	return total, nil
}

// BadgeSvg: GET /badge/svg/:uuid?days (public) -> proxied SVG from shields.io.
func (h *Handler) BadgeSvg(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("svg"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("Invalid badge id"))
	}
	ctx := c.Request().Context()

	user, project, ok, err := h.DB.GetBadgeLinkInfo(ctx, id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "badge link lookup failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("Badge not found"))
	}

	// boom-6jm.3: apply the owner's hide rules before hitting the DB for
	// activity totals. If the badge's subject project has been curated away,
	// the badge itself must 404 — otherwise the shields.io label leaks the
	// project name and the total leaks per-day activity.
	hidden, err := h.DB.LoadHiddenSets(ctx, user)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "badge hidden sets load failed", err)
	}
	if applyBadgeCuration(hidden, project) == "hidden" {
		return apihelpers.RespondErr(c, apierr.NotFound("Badge not found"))
	}

	days := apihelpers.QueryInt64(c, "days", 7)
	total, err := h.badgeTotalSeconds(ctx, user, project, days, hidden)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "badge activity query failed", err)
	}

	message := stats.CompoundDuration(&total)
	shieldURL := h.Cfg.ShieldsIOURL + "/static/v1?label=" + url.QueryEscape(project) +
		"&message=" + url.QueryEscape(message) + "&color=blue"

	resp, err := httpClient.Get(shieldURL)
	if err != nil {
		h.Logger.Error("shields.io request failed", "err", err)
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadGateway, "Badge upstream request failed", nil))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.Logger.Warn("shields.io returned non-200", "status", resp.StatusCode)
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadGateway, "Badge upstream request failed", nil))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "badge upstream read failed", err)
	}
	return c.Blob(http.StatusOK, "image/svg+xml", body)
}
