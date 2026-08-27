// handler.go — HTTP handlers for user-defined composite goals (boom-wpb).
//
// All routes are owner-scoped via apihelpers.IdentifyOwner(h.DB, c). Cross-owner id
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
//
// Every route is registered through internal/shared/apiroute (see
// routes.go), which is what lets the OpenAPI spec carry a real schema:
// the request/response types are captured at the registration call
// site, so the docs cannot drift from the handler. That is also why the
// response envelopes below are named types rather than the
// map[string]any literals they used to be — a map has no shape to
// reflect.
package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// Handler bundles the per-domain HTTP handler dependencies (boom-8tn
// phase 2b). Only holds the fields the goals domain actually reads.
type Handler struct {
	DB     *db.DB
	Logger *slog.Logger
}

// createGoalRequest is the POST body. Description is optional; empty
// string means "no description" (server stores NULL). Spec is a raw
// JSONB blob; the validator parses it before any DB touch.
//
// Bound by the apiroute seam under apihelpers.BodyLimitSmall — the same
// cap this handler applied by hand. Small cap: name + description + a
// modest spec tree. The MEDIUM cap would work but SMALL is a tighter
// honest ceiling — a spec that needs 4 KiB has too many predicates.
type createGoalRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
	// Public opts the goal into the owner's embeddable goal widgets
	// (Part B Stage 4). Omitted/false means private, matching the
	// column default — a plain bool (not *bool) is fine here since
	// creation has no "leave untouched" case to distinguish.
	Public bool `json:"public"`
}

// updateGoalRequest is the PATCH body. Every field is a pointer so we
// can distinguish "unset" from "empty string / false". A non-nil Spec
// re-runs ValidateSpec + clears the progress cache.
type updateGoalRequest struct {
	Name        *string          `json:"name"`
	Description *string          `json:"description"`
	Spec        *json.RawMessage `json:"spec"`
	Enabled     *bool            `json:"enabled"`
	Public      *bool            `json:"public"`
}

// toggleGoalRequest matches the curation toggle shape — a plain flip
// when body is absent, exact-set when `enabled` is present.
type toggleGoalRequest struct {
	Enabled *bool `json:"enabled"`
}

// Response envelopes. These were map[string]any literals; the json tags
// below reproduce the old map keys EXACTLY, so the wire format is
// unchanged.

// listGoalsResponse is GET /api/v1/users/current/goals.
type listGoalsResponse struct {
	// Goals is every goal owned by the caller, newest first.
	Goals []Goal `json:"goals"`
}

// goalResponse is the single-goal envelope shared by
// GET/POST/PATCH on /api/v1/users/current/goals[/:id].
type goalResponse struct {
	Goal *Goal `json:"goal"`
}

// toggleGoalResponse is POST /api/v1/users/current/goals/:id/toggle —
// the goal's enabled state AFTER the flip/set.
type toggleGoalResponse struct {
	Enabled bool `json:"enabled"`
}

// allGoalProgressResponse is GET /api/v1/users/current/goals/progress.
type allGoalProgressResponse struct {
	// Progress is goal id -> evaluated progress. Disabled goals and
	// goals whose spec failed to evaluate are absent from the map; the
	// tile renderer treats a missing entry as "unknown/no data".
	Progress map[string]*Progress `json:"progress"`
}

// ListGoals: GET /api/v1/users/current/goals → {goals:[Goal]}.
func (h *Handler) ListGoals(c *echo.Context) (listGoalsResponse, error) {
	var out listGoalsResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	goals, err := ListGoals(h.DB, c.Request().Context(), owner)
	if err != nil {
		return out, fmt.Errorf("list goals failed: %w", err)
	}
	return listGoalsResponse{Goals: goals}, nil
}

// GetGoal: GET /api/v1/users/current/goals/:id → {goal:Goal}.
// Owner-scoped: cross-owner id returns 404 (no oracle).
func (h *Handler) GetGoal(c *echo.Context) (goalResponse, error) {
	var out goalResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id := c.Param("id")
	g, err := GetGoal(h.DB, c.Request().Context(), owner, id)
	if err != nil {
		return out, fmt.Errorf("get goal failed: %w", err)
	}
	if g == nil {
		return out, apierr.NotFound("goal not found")
	}
	return goalResponse{Goal: g}, nil
}

// CreateGoal: POST /api/v1/users/current/goals → {goal:Goal}.
// Body: {name, description?, spec}. Validates spec strictly; a bad
// spec is 400 with the error text so the author can fix it. Duplicate
// (owner, name) is 409.
func (h *Handler) CreateGoal(c *echo.Context, req createGoalRequest) (goalResponse, error) {
	var out goalResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if strings.TrimSpace(req.Name) == "" {
		return out, apierr.BadRequest("name is required")
	}
	if len(req.Spec) == 0 {
		return out, apierr.BadRequest("spec is required")
	}
	if _, err := ValidateSpec(req.Spec); err != nil {
		return out, apierr.BadRequest(err.Error())
	}
	var descPtr *string
	if req.Description != "" {
		descPtr = &req.Description
	}
	g, err := CreateGoal(h.DB, c.Request().Context(), owner, req.Name, descPtr, req.Spec, req.Public)
	if err != nil {
		if isUniqueViolation(err) {
			return out, apierr.New(http.StatusConflict, "a goal named "+req.Name+" already exists", nil)
		}
		return out, fmt.Errorf("create goal failed: %w", err)
	}
	h.Logger.Info("goal created", "user", owner, "goal", g.ID)
	return goalResponse{Goal: g}, nil
}

// UpdateGoal: PATCH /api/v1/users/current/goals/:id → {goal:Goal}.
// Only touched fields are written. A spec write clears the cached
// progress (invariant enforced by db.UpdateGoal). Cross-owner id
// returns 404 (indistinguishable from "no such id"). Duplicate
// (owner, name) on rename is 409.
func (h *Handler) UpdateGoal(c *echo.Context, req updateGoalRequest) (goalResponse, error) {
	var out goalResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id := c.Param("id")
	if req.Spec != nil {
		if len(*req.Spec) == 0 {
			return out, apierr.BadRequest("spec cannot be empty")
		}
		if _, err := ValidateSpec(*req.Spec); err != nil {
			return out, apierr.BadRequest(err.Error())
		}
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return out, apierr.BadRequest("name cannot be empty")
	}
	patch := GoalPatch{
		Name:        req.Name,
		Description: req.Description,
		Spec:        req.Spec,
		Enabled:     req.Enabled,
		Public:      req.Public,
	}
	g, err := UpdateGoal(h.DB, c.Request().Context(), owner, id, patch)
	if err != nil {
		if isUniqueViolation(err) {
			return out, apierr.New(http.StatusConflict, "a goal with that name already exists", nil)
		}
		return out, fmt.Errorf("update goal failed: %w", err)
	}
	if g == nil {
		return out, apierr.NotFound("goal not found")
	}
	h.Logger.Info("goal updated", "user", owner, "goal", g.ID)
	return goalResponse{Goal: g}, nil
}

// DeleteGoal: DELETE /api/v1/users/current/goals/:id → 204.
// Idempotent-in-effect for cross-owner or already-deleted ids: still
// 404, no distinguisher, no oracle.
//
// Registered via apiroute.NoContent, which writes the 204 — a nil
// return here IS the success case.
func (h *Handler) DeleteGoal(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	id := c.Param("id")
	ok, err := DeleteGoal(h.DB, c.Request().Context(), owner, id)
	if err != nil {
		return fmt.Errorf("delete goal failed: %w", err)
	}
	if !ok {
		return apierr.NotFound("goal not found")
	}
	h.Logger.Info("goal deleted", "user", owner, "goal", id)
	return nil
}

// ToggleGoal: POST /api/v1/users/current/goals/:id/toggle → {enabled:bool}.
// Body optional: omit to flip, {"enabled":bool} to set exactly.
// Idempotent — an exact-set matching the current value returns 200.
//
// The seam binds the body before this runs; echo's binder is a no-op on
// a zero-length body, so an absent body still leaves req.Enabled nil
// (the flip case) exactly as the hand-rolled ContentLength check did.
func (h *Handler) ToggleGoal(c *echo.Context, req toggleGoalRequest) (toggleGoalResponse, error) {
	var out toggleGoalResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id := c.Param("id")
	enabled, found, err := ToggleGoal(h.DB, c.Request().Context(), owner, id, req.Enabled)
	if err != nil {
		return out, fmt.Errorf("toggle goal failed: %w", err)
	}
	if !found {
		return out, apierr.NotFound("goal not found")
	}
	h.Logger.Info("goal toggled", "user", owner, "goal", id, "enabled", enabled)
	return toggleGoalResponse{Enabled: enabled}, nil
}

// GetGoalProgress: GET /api/v1/users/current/goals/:id/progress → Progress.
// Serve-from-cache when last_evaluated_at is within GoalCacheTTL of
// now; otherwise recompute and cache. Explicit invalidation (spec
// PATCH, heartbeat ingest) sets last_evaluated_at NULL so the next
// read always recomputes.
//
// The body is a bare Progress — no envelope — which is why this returns
// *Progress rather than a wrapper type.
func (h *Handler) GetGoalProgress(c *echo.Context) (*Progress, error) {
	var out *Progress
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id := c.Param("id")
	g, err := GetGoal(h.DB, c.Request().Context(), owner, id)
	if err != nil {
		return out, fmt.Errorf("get goal (for progress) failed: %w", err)
	}
	if g == nil {
		return out, apierr.NotFound("goal not found")
	}
	prog, err := h.evalGoal(c, g)
	if err != nil {
		return out, apierr.BadRequest(err.Error())
	}
	return prog, nil
}

// GetAllGoalProgress: GET /api/v1/users/current/goals/progress
//
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
func (h *Handler) GetAllGoalProgress(c *echo.Context) (allGoalProgressResponse, error) {
	var resp allGoalProgressResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return resp, aerr
	}
	goals, err := ListGoals(h.DB, c.Request().Context(), owner)
	if err != nil {
		return resp, fmt.Errorf("list goals (for batch progress) failed: %w", err)
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
	return allGoalProgressResponse{Progress: out}, nil
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

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). Mirror of the same helper in
// internal/widgets/widget_defs.go — both are 3-line file-local helpers
// used by exactly one CRUD path each, so DRY-ing to a shared package
// would trade one indirection for one line saved.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
