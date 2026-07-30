// handler.go — HTTP handlers for user-defined composite goals (gaka-wpb).
//
// All routes are owner-scoped via h.resolveUser(c). Cross-owner id
// access returns 404 — never 403 — so an attacker can't distinguish
// "no such goal" from "not yours" (no oracle). Mirrors the same rule
// as curation and dashboard_layout.
//
// Predicate validation lives in internal/goals/eval.go
// (ValidateSpec); this handler surfaces its error text on 400 so
// authors can correct their spec. The cache-freshness policy
// (GoalCacheTTL) also lives there — this handler only reads the two
// cache columns (last_progress, last_evaluated_at) and decides
// serve-from-cache vs recompute.
//
// The batched progress endpoint (/goals/progress) is what dashboard
// widgets hit — one HTTP round trip per dashboard render, not N per
// goal tile.
package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// Handler bundles the per-domain HTTP handler dependencies (gaka-8tn
// phase 2b). Only holds the fields the goals domain actually reads.
type Handler struct {
	DB     *db.DB
	Logger *slog.Logger
}

// Body-size caps — mirrored from internal/handler.BodyLimit* while the
// shared apihelpers package is being introduced in a parallel phase.
const bodyLimitSmall int64 = 4 * 1024

// createGoalRequest is the POST body. Description is optional; empty
// string means "no description" (server stores NULL). Spec is a raw
// JSONB blob; the validator parses it before any DB touch.
type createGoalRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
}

// updateGoalRequest is the PATCH body. Every field is a pointer so we
// can distinguish "unset" from "empty string / false". A non-nil Spec
// re-runs ValidateSpec + clears the progress cache.
type updateGoalRequest struct {
	Name        *string          `json:"name"`
	Description *string          `json:"description"`
	Spec        *json.RawMessage `json:"spec"`
	Enabled     *bool            `json:"enabled"`
}

// toggleGoalRequest matches the curation toggle shape — a plain flip
// when body is absent, exact-set when `enabled` is present.
type toggleGoalRequest struct {
	Enabled *bool `json:"enabled"`
}

// ListGoals: GET /api/v1/users/current/goals → {goals:[Goal]}.
func (h *Handler) ListGoals(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	goals, err := ListGoals(h.DB, c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "list goals failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"goals": goals})
}

// GetGoal: GET /api/v1/users/current/goals/:id → {goal:Goal}.
// Owner-scoped: cross-owner id returns 404 (no oracle).
func (h *Handler) GetGoal(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	g, err := GetGoal(h.DB, c.Request().Context(), owner, id)
	if err != nil {
		return h.internalErr(c, "get goal failed", err)
	}
	if g == nil {
		return respondErr(c, apierr.NotFound("goal not found"))
	}
	return c.JSON(http.StatusOK, map[string]any{"goal": g})
}

// CreateGoal: POST /api/v1/users/current/goals → {goal:Goal}.
// Body: {name, description?, spec}. Validates spec strictly; a bad
// spec is 400 with the error text so the author can fix it. Duplicate
// (owner, name) is 409.
func (h *Handler) CreateGoal(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req createGoalRequest
	// Small cap: name + description + a modest spec tree. The MEDIUM
	// cap would work but SMALL is a tighter honest ceiling — a spec
	// that needs 4 KiB has too many predicates.
	if aerr := bindJSONWithLimit(c, &req, bodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	if strings.TrimSpace(req.Name) == "" {
		return respondErr(c, apierr.BadRequest("name is required"))
	}
	if len(req.Spec) == 0 {
		return respondErr(c, apierr.BadRequest("spec is required"))
	}
	if _, err := ValidateSpec(req.Spec); err != nil {
		return respondErr(c, apierr.BadRequest(err.Error()))
	}
	var descPtr *string
	if req.Description != "" {
		descPtr = &req.Description
	}
	g, err := CreateGoal(h.DB, c.Request().Context(), owner, req.Name, descPtr, req.Spec)
	if err != nil {
		if isUniqueViolation(err) {
			return respondErr(c, apierr.New(http.StatusConflict, "a goal named "+req.Name+" already exists", nil))
		}
		return h.internalErr(c, "create goal failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"goal": g})
}

// UpdateGoal: PATCH /api/v1/users/current/goals/:id → {goal:Goal}.
// Only touched fields are written. A spec write clears the cached
// progress (invariant enforced by db.UpdateGoal). Cross-owner id
// returns 404 (indistinguishable from "no such id"). Duplicate
// (owner, name) on rename is 409.
func (h *Handler) UpdateGoal(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	var req updateGoalRequest
	if aerr := bindJSONWithLimit(c, &req, bodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	if req.Spec != nil {
		if len(*req.Spec) == 0 {
			return respondErr(c, apierr.BadRequest("spec cannot be empty"))
		}
		if _, err := ValidateSpec(*req.Spec); err != nil {
			return respondErr(c, apierr.BadRequest(err.Error()))
		}
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return respondErr(c, apierr.BadRequest("name cannot be empty"))
	}
	patch := GoalPatch{
		Name:        req.Name,
		Description: req.Description,
		Spec:        req.Spec,
		Enabled:     req.Enabled,
	}
	g, err := UpdateGoal(h.DB, c.Request().Context(), owner, id, patch)
	if err != nil {
		if isUniqueViolation(err) {
			return respondErr(c, apierr.New(http.StatusConflict, "a goal with that name already exists", nil))
		}
		return h.internalErr(c, "update goal failed", err)
	}
	if g == nil {
		return respondErr(c, apierr.NotFound("goal not found"))
	}
	return c.JSON(http.StatusOK, map[string]any{"goal": g})
}

// DeleteGoal: DELETE /api/v1/users/current/goals/:id → 204.
// Idempotent-in-effect for cross-owner or already-deleted ids: still
// 404, no distinguisher, no oracle.
func (h *Handler) DeleteGoal(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	ok, err := DeleteGoal(h.DB, c.Request().Context(), owner, id)
	if err != nil {
		return h.internalErr(c, "delete goal failed", err)
	}
	if !ok {
		return respondErr(c, apierr.NotFound("goal not found"))
	}
	return c.NoContent(http.StatusNoContent)
}

// ToggleGoal: POST /api/v1/users/current/goals/:id/toggle → {enabled:bool}.
// Body optional: omit to flip, {"enabled":bool} to set exactly.
// Idempotent — an exact-set matching the current value returns 200.
func (h *Handler) ToggleGoal(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	var req toggleGoalRequest
	if c.Request().ContentLength > 0 {
		if aerr := bindJSONWithLimit(c, &req, bodyLimitSmall); aerr != nil {
			return respondErr(c, aerr)
		}
	}
	enabled, found, err := ToggleGoal(h.DB, c.Request().Context(), owner, id, req.Enabled)
	if err != nil {
		return h.internalErr(c, "toggle goal failed", err)
	}
	if !found {
		return respondErr(c, apierr.NotFound("goal not found"))
	}
	return c.JSON(http.StatusOK, map[string]any{"enabled": enabled})
}

// GetGoalProgress: GET /api/v1/users/current/goals/:id/progress → Progress.
// Serve-from-cache when last_evaluated_at is within GoalCacheTTL of
// now; otherwise recompute and cache. Explicit invalidation (spec
// PATCH, heartbeat ingest) sets last_evaluated_at NULL so the next
// read always recomputes.
func (h *Handler) GetGoalProgress(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	id := c.Param("id")
	g, err := GetGoal(h.DB, c.Request().Context(), owner, id)
	if err != nil {
		return h.internalErr(c, "get goal (for progress) failed", err)
	}
	if g == nil {
		return respondErr(c, apierr.NotFound("goal not found"))
	}
	prog, err := h.evalGoal(c, g)
	if err != nil {
		return respondErr(c, apierr.BadRequest(err.Error()))
	}
	return c.JSON(http.StatusOK, prog)
}

// GetAllGoalProgress: GET /api/v1/users/current/goals/progress
//	→ {progress: {id -> Progress}}.
//
// One HTTP round trip serves every dashboard tile — the FE calls this
// once per dashboard render and hands each tile the pre-computed
// entry from the map. Cache TTL applies per-goal (each row's own
// last_evaluated_at gates freshness); a stale row is recomputed on
// this call and its cache updated so the NEXT batch benefits.
//
// Disabled goals are skipped — the tile renderer treats a missing
// entry as "unknown/no data".
func (h *Handler) GetAllGoalProgress(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	goals, err := ListGoals(h.DB, c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "list goals (for batch progress) failed", err)
	}
	out := map[string]*Progress{}
	for i := range goals {
		g := &goals[i]
		if !g.Enabled {
			continue
		}
		p, err := h.evalGoal(c, g)
		if err != nil {
			// Skip broken specs in the batch — don't fail the whole
			// response. The single-goal endpoint reports the error
			// text so the author can debug there.
			h.Logger.Warn("goal eval failed in batch", "goal", g.ID, "err", err)
			continue
		}
		out[g.ID] = p
	}
	return c.JSON(http.StatusOK, map[string]any{"progress": out})
}

// evalGoal encapsulates the cache-freshness decision + eval + cache
// write. Shared by the single-id and batch endpoints so the freshness
// contract is stated ONCE.
//
// Returns the Progress the caller should return. Errors surface as
// validate-time issues (400 upstream); DB errors are logged and
// bubbled here as errors too — the caller decides response shape.
func (h *Handler) evalGoal(c *echo.Context, g *Goal) (*Progress, error) {
	// Cache path: if we have a non-nil last_progress AND the cache
	// timestamp is fresh, decode + return it. A cache row with a
	// missing timestamp is treated as stale (belt-and-suspenders).
	if len(g.LastProgress) > 0 && g.LastEvaluatedAt != nil {
		if time.Since(*g.LastEvaluatedAt) < GoalCacheTTL {
			cached, err := UnmarshalProgress(g.LastProgress)
			if err == nil && cached != nil {
				return cached, nil
			}
			// Fall through to recompute on corrupted cache.
		}
	}
	// Recompute path: re-validate spec (defensive; a spec that survived
	// CreateGoal/UpdateGoal is already valid, but if a manual DB write
	// bypassed us, we don't want to walk garbage).
	p, err := ValidateSpec(g.Spec)
	if err != nil {
		return nil, err
	}
	prog, err := Evaluate(c.Request().Context(), h.DB.Pool, g.Owner, p, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	// Persist to cache (best-effort — a failure here doesn't sink the
	// response, we just log and move on; the next call recomputes).
	if raw, mErr := MarshalProgress(prog); mErr == nil {
		if wErr := UpdateGoalProgress(h.DB, c.Request().Context(), g.Owner, g.ID, raw); wErr != nil {
			h.Logger.Warn("goal cache write failed (non-fatal)", "goal", g.ID, "err", wErr)
		}
	}
	return prog, nil
}

// ---- Local helpers (mirror internal/handler helpers until apihelpers lands) ----

// respondErr renders an apierr.Error onto the context.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return e.Write(c)
}

// resolveUser maps a token to its owning username. Mirrors the version in
// internal/handler until the shared apihelpers package lands.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	tkn, ok := auth.ParseAuthHeader(c.Request().Header.Get(echo.HeaderAuthorization))
	if !ok || tkn == "" {
		return "", "", apierr.MissingAuth()
	}
	owner, ok, err := h.DB.GetUserByToken(c.Request().Context(), tkn)
	if err != nil {
		return "", "", apierr.Generic()
	}
	if !ok {
		return "", "", apierr.InvalidToken()
	}
	return tkn, owner, nil
}

// internalErr logs the underlying error with request context and renders
// the generic 500 envelope.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	h.Logger.Error(msg, "path", c.Request().URL.Path, "err", err)
	return respondErr(c, apierr.Generic())
}

// bindJSONWithLimit wraps c.Bind with a http.MaxBytesReader cap on the
// request body. Mirror of internal/handler.BindJSONWithLimit until the
// shared apihelpers package lands.
func bindJSONWithLimit(c *echo.Context, dst any, limit int64) *apierr.Error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limit)
	if err := c.Bind(dst); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			extra := fmt.Sprintf("limit=%d", limit)
			return apierr.New(http.StatusRequestEntityTooLarge, "payload too large", &extra)
		}
		return apierr.BadRequest("Invalid request body")
	}
	return nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). Mirror of the same helper in
// internal/handler/widget_defs.go.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
