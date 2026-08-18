// handler.go: the embeddable-widget endpoints (gaka-hsj). Auth'd link CRUD
// (mint/list/delete) plus the PUBLIC SVG renderer. The public endpoint is the
// privacy-sensitive one: it must apply the owner's hide/rename curation so
// curated-away data never leaks into a README embed.
package widgets

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/stats"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/widget"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// isWidgetScopeProjectHidden is the widget-endpoint analogue of
// applyBadgeCuration (see badges.go). Widgets whose scope is a specific project
// have cardinality-1 subject-matter — if the pinned project is on the owner's
// hide list, there is no partially-scrubbed representation the renderer could
// emit that doesn't leak the project name (via the title/subtitle) or its
// activity (via top-N rows for that single project). The endpoint MUST 404
// instead. Mirrors applyBadgeCuration's case-insensitive semantics: hide values
// are stored lowercased by db.LoadHiddenSets and compared via lower(col).
//
// hidden may be nil (defensive; the handler never passes nil in production).
func isWidgetScopeProjectHidden(hidden model.HiddenSets, project string) bool {
	if hidden == nil {
		return false
	}
	needle := strings.ToLower(project)
	for _, hp := range hidden.Projects() {
		if hp == needle {
			return true
		}
	}
	return false
}

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
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
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
			return apihelpers.InternalErr(h.Logger, c, "widget link project check failed", err)
		}
		if !ok {
			// gaka-xuc: the FE gets remapped project names from ProjectList
			// (which applies loadRenames), but the raw projects table only
			// carries source names. Accept scopeRef when it is the target of
			// an exact rename rule — the widget renderer expands the
			// scope-ref back to the source project(s) at query time.
			rs, err := h.DB.LoadRenameSets(ctx, owner)
			if err != nil {
				return apihelpers.InternalErr(h.Logger, c, "widget link rename load failed", err)
			}
			if len(rs.ExactSourcesFor("project", scopeRef)) == 0 {
				return apihelpers.RespondErr(c, apierr.NotFound("Unknown project"))
			}
		}
	case db.WidgetScopeSpace:
		id, err := strconv.Atoi(scopeRef)
		if err != nil {
			return apihelpers.RespondErr(c, apierr.BadRequest("Invalid space id"))
		}
		sp, _, err := h.DB.GetSpace(ctx, owner, id)
		if err != nil {
			return apihelpers.InternalErr(h.Logger, c, "widget link space check failed", err)
		}
		if sp == nil {
			return apihelpers.RespondErr(c, apierr.NotFound("Unknown space"))
		}
	default:
		return apihelpers.RespondErr(c, apierr.BadRequest("scopeType must be user, project or space"))
	}

	id, err := h.DB.CreateWidgetLink(ctx, owner, scopeType, scopeRef)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "widget link creation failed", err)
	}
	return c.JSON(http.StatusOK, model.WidgetLinkResponse{
		WidgetBaseURL: h.Cfg.BadgeURL + "/widget/svg/" + id.String(),
		LinkID:        id.String(),
	})
}

// WidgetLinkList: GET /api/v1/users/current/widgets/links (auth) — Settings UI.
func (h *Handler) WidgetLinkList(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	links, err := h.DB.ListWidgetLinks(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "widget link list failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"links": links})
}

// WidgetLinkRoll: POST /api/v1/users/current/widgets/link/:id/roll (auth).
// Mints a new uuid for the same (user, scope). Returns the new URL — old id
// immediately 404s (existing embeds break; the point is exactly to break
// them). Owner-scoped: cross-owner ids 404.
func (h *Handler) WidgetLinkRoll(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	oldID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("Invalid widget link id"))
	}
	newID, ok, err := h.DB.RollWidgetLink(c.Request().Context(), owner, oldID)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "widget link roll failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("Widget link not found"))
	}
	// Any previously-cached bytes lived under the old id in the cache key, so
	// they can't accidentally be served post-roll — but invalidate defensively
	// (cheap; owner-prefixed sweep).
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
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
		return apihelpers.RespondErr(c, apierr.BadRequest("Invalid widget link id"))
	}
	kind := c.Param("kind")
	// gaka-567: "custom" is the builder-composed kind — spec is inline in the
	// URL as ?spec=<base64>. Every other kind must be a target:"both" spec
	// (widget.IsKind — Part B Stage 5: this now covers every renderable
	// kind, including the goal-* kinds, since renderSpec is the only path).
	var customDef *widget.Def
	if widget.IsCustomKind(kind) {
		def, err := widget.DecodeDef(c.QueryParam("spec"))
		if err != nil {
			return apihelpers.RespondErr(c, apierr.BadRequest("Invalid widget spec: "+err.Error()))
		}
		customDef = &def
	} else if !widget.IsKind(kind) {
		return apihelpers.RespondErr(c, apierr.NotFound("Unknown widget kind"))
	}
	ctx := c.Request().Context()

	owner, scopeType, scopeRef, ok, err := h.DB.GetWidgetLinkInfo(ctx, id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "widget link lookup failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("Widget link not found"))
	}

	// gaka-6jm.5: for project-scoped widgets, the pinned project name is baked
	// into the widget's identity (URL + title/subtitle) — if it has been curated
	// away, ANY response leaks either the name (via chrome) or its activity
	// (via top-N rows on a single-project scope, which the DB predicate won't
	// exclude, since the scope IS that project). Mirror the badge endpoint's
	// 404 policy so an outsider can't enumerate curated project names by probing
	// minted widget ids. Runs BEFORE cachedBlob — a 404 has no cacheable payload
	// and cachedBlob would swallow the apierr as a generic 500.
	if scopeType == db.WidgetScopeProject {
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return apihelpers.InternalErr(h.Logger, c, "widget hidden sets load failed", err)
		}
		if isWidgetScopeProjectHidden(hidden, scopeRef) {
			return apihelpers.RespondErr(c, apierr.NotFound("Widget link not found"))
		}
	}

	// Part B Stage 4 privacy fix: goals are account-wide, not scoped to a
	// project or space. publicGoalsFor always returns the OWNER's public
	// goals regardless of the link's scope, so a project/space-scoped link
	// (minted for one project's README) must not be allowed to render a
	// goal-* kind — that would expose the owner's account-wide public goals
	// under a URL an outsider would reasonably assume is project-scoped.
	// widget.IsGoalKind(kind) is exactly the goal-* set today; if a future
	// "both" kind is legitimately non-user-scoped for an unrelated reason,
	// this gate needs to narrow to a goal-specific predicate instead. 404,
	// not a partial render — mirrors the project-hidden gate above, no oracle.
	if widget.IsGoalKind(kind) && scopeType != db.WidgetScopeUser {
		return apihelpers.RespondErr(c, apierr.NotFound("Widget link not found"))
	}

	// Track the request so the Settings badge can show "last requested Nm ago"
	// and its click-through popover can list unique origins. Best-effort:
	// don't fail the render if the hit-record write hits an issue.
	if err := h.DB.RecordWidgetLinkHit(ctx, id, c.Request().Referer()); err != nil {
		h.Logger.Debug("record widget hit failed", "id", id, "err", err)
	}

	days := apihelpers.QueryInt64(c, "days", widgetDaysDefault)
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

	key := apihelpers.CacheKey(owner, "widget", id.String(), kind, days, theme, title, spec)
	return apihelpers.CachedBlob(h.Cache, h.Logger, c, key, "image/svg+xml", func() ([]byte, error) {
		t1 := time.Now().UTC()
		t0 := removeDays(t1, int(days))

		// Privacy: ALWAYS apply the owner's curation to the public payload.
		// (Project-scope hide → 404 already enforced above, before cachedBlob.)
		hidden, err := h.DB.LoadHiddenSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		renames, err := h.DB.LoadRenameSets(ctx, owner)
		if err != nil {
			return nil, err
		}
		// gaka-dg7: widget uses the OWNER's tz (the widget shows the owner's
		// data in the owner's timeframe — a public embed of a Pacific dev's
		// punchcard should reflect Pacific dow/hour buckets even when a UTC
		// requester loads the SVG). The cache key does NOT need tz appended
		// because the owner-prefixed sweep already invalidates on a TZ change.
		tz := apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, owner, h.Cfg.DefaultTimezone)

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
			rows, err = h.DB.GetUserActivity(ctx, owner, t0, t1, widgetTimeLimit, tz, hidden, renames, members, scoped)
		}
		if err != nil {
			return nil, err
		}

		// Part B Stage 5 cutover: renderSpec/NeedsForSpec (spec.go) is the
		// ONLY render path for every "both"-target kind — the flagged
		// legacy/spec-engine split (BOOM_WIDGET_SPEC_ENGINE) is gone. The
		// "custom" (Def-based builder) path is untouched; it isn't part of
		// the spec registry, so it derives its Needs separately.
		var needs widget.Requirements
		if customDef != nil {
			needs = widget.NeedsForDef(*customDef)
		} else {
			kindSpec, ok := widget.SpecFor(kind)
			if !ok || kindSpec.Target != widget.TargetBoth {
				return nil, fmt.Errorf("widget: %q is not a renderable spec kind", kind)
			}
			needs = widget.NeedsForSpec(kindSpec)
		}

		// Part B Stage 1: the Categories segment isn't derivable from the
		// StatRow set (the activity queries don't project a category column),
		// so it needs its own fetch — gated on Needs so only categories-chart
		// pays for it. Same rollup-vs-raw gate as the activity fetch above.
		var catRows []db.CategoryDailyRow
		if needs.Categories {
			if !hidden.HasHiddenOutside(db.RollupAxes) && (!scoped || !members.HasMemberOutside(db.RollupAxes)) {
				catRows, err = h.DB.GetCategoryDailyRollup(ctx, owner, t0, t1, hidden, renames, members, scoped)
			} else {
				catRows, err = h.DB.GetCategoryDaily(ctx, owner, t0, t1, widgetTimeLimit, tz, hidden, renames, members, scoped)
			}
			if err != nil {
				return nil, err
			}
		}

		payload := stats.ToStatsPayload(t0, t1, rows, catRows, nil)
		// gaka-6jm.3: enforce the public-safe contract before ANY renderer sees
		// the payload. The DB queries above already excluded hidden values
		// from top-N segments; Scrub additionally strips hidden names from
		// the OtherMembers tail that capWithOther collapses in application
		// code, so a hidden project/language/etc. can never surface via the
		// "Other (N more)" bucket the FE tooltip breakdown would expose.
		scrubbed := widget.Scrub(&payload, hidden)
		data := &widget.Data{Payload: scrubbed}
		if needs.Grade {
			g := stats.Grade(&payload)
			data.Grade = &g
		}
		if needs.Punchcard {
			cells, err := h.DB.GetPunchcard(ctx, owner, t0, t1, widgetTimeLimit, tz, hidden, members, scoped)
			if err != nil {
				return nil, err
			}
			pc := stats.ToPunchcardPayload(cells)
			data.Punchcard = &pc
		}
		if needs.Momentum {
			mrows, err := h.DB.GetMomentum(ctx, owner, t0, t1, widgetTimeLimit, tz, hidden, renames, members, scoped)
			if err != nil {
				return nil, err
			}
			mp := stats.ToMomentumPayload(t0, t1, mrows, 6)
			// gaka-6jm.6: enforce the public-safe contract on momentum too. The
			// DB predicate above already excluded hidden projects at query time;
			// ScrubMomentum is the belt-and-braces guard against any drift
			// between the DB hide set and the render pipeline (mirrors the
			// Scrub call on the StatsPayload above).
			data.Momentum = widget.ScrubMomentum(&mp, hidden)
		}
		if needs.Sessions {
			srows, err := h.DB.GetSessions(ctx, owner, t0, t1, widgetTimeLimit, tz, hidden, members, scoped)
			if err != nil {
				return nil, err
			}
			sp := stats.ToSessionsPayload(t0, t1, srows)
			data.Sessions = &sp
		}
		if needs.Goals {
			gl, err := publicGoalsFor(ctx, h.DB, owner)
			if err != nil {
				return nil, err
			}
			data.Goals = gl
		}
		opts := widget.Options{
			Theme:    theme,
			Title:    title,
			Subtitle: fmt.Sprintf("last %d days", days),
		}
		if customDef != nil {
			return widget.RenderCustom(data, *customDef, opts)
		}
		return widget.RenderSpec(kind, data, opts)
	})
}

// publicGoalsFor resolves the link owner's PUBLIC, ENABLED goals + their
// progress for a goal-* embed (Part B Stage 4). PRIVACY: this is the ONLY
// place the widget endpoint touches goal data. The PRIMARY gate is SQL —
// goals.ListPublicGoals filters `enabled = true AND public = true` in the
// query itself, so a private goal's row (spec JSONB included) never leaves
// Postgres on this public, unauthenticated path. The Enabled/Public re-check
// below is belt-and-braces defense-in-depth (should never actually trip
// given the WHERE clause) — never rely on it alone. Progress prefers the
// cached last_progress column (the same cache the authenticated dashboard
// reads) to keep the public endpoint cheap; a goal that has never been
// evaluated (nil cache — e.g. freshly created, never opened on the
// dashboard) is evaluated once here so the first embed view isn't a
// permanent blank. A goal whose spec fails to validate or evaluate is
// skipped (best-effort, mirrors the batched /goals/progress handler) rather
// than failing the whole widget render.
func publicGoalsFor(ctx context.Context, d *db.DB, owner string) ([]widget.GoalProgressLite, error) {
	all, err := goals.ListPublicGoals(d, ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]widget.GoalProgressLite, 0, len(all))
	for i := range all {
		g := &all[i]
		if !g.Enabled || !g.Public {
			// Defense-in-depth only — ListPublicGoals' WHERE clause should
			// already guarantee this never fires. See doc comment above.
			continue
		}
		var prog *goals.Progress
		if len(g.LastProgress) > 0 {
			if cached, cErr := goals.UnmarshalProgress(g.LastProgress); cErr == nil {
				prog = cached
			}
			// A corrupted cache falls through to recompute below.
		}
		if prog == nil {
			p, vErr := goals.ValidateSpec(g.Spec)
			if vErr != nil {
				continue
			}
			evaluated, eErr := goals.Evaluate(ctx, d.Pool, owner, p, time.Now().UTC())
			if eErr != nil {
				continue
			}
			prog = evaluated
			// Best-effort cache warm so the NEXT embed view (and the owner's
			// own dashboard) benefits — a write failure here doesn't sink
			// this render.
			if raw, mErr := goals.MarshalProgress(prog); mErr == nil {
				_ = goals.UpdateGoalProgress(d, ctx, owner, g.ID, raw)
			}
		}
		out = append(out, widget.GoalProgressLite{
			Name:     g.Name,
			Progress: prog.Progress,
			Hit:      prog.Hit,
		})
	}
	return out, nil
}
