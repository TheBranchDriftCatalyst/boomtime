package curation

import (
	"context"
	"net/http"
	"strconv"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// curationRequest is the POST body for creating a rule.
//
// boom-bi2: bound under a bounded MaxBytesReader cap — curation rules are
// compact JSON (axis, action, matchType, matchValue, optional newValue);
// pattern strings should never approach that bound. The bind now happens at
// the apiroute seam (apihelpers.BodyLimitSmall) rather than in the handler.
type curationRequest struct {
	Axis       string  `json:"axis"`
	Action     string  `json:"action"`
	MatchType  string  `json:"matchType"` // "exact" (default) | "regex" | "template"
	MatchValue string  `json:"matchValue"`
	NewValue   *string `json:"newValue"`
	// ApplyAtIngest (boom-scrub) marks a rename rule that also rewrites newly-
	// ingested heartbeats (the "scrubber"). Rename-only; validated to compile
	// under Go RE2 (the ingest apply engine) below.
	ApplyAtIngest bool `json:"applyAtIngest"`
}

// listCurationResponse is GET /api/v1/users/current/curation.
type listCurationResponse struct {
	Rules []db.CurationRule `json:"rules"`
}

// ListCuration: GET /api/v1/users/current/curation → {rules:[CurationRule]}.
func (h *Handler) ListCuration(c *echo.Context) (listCurationResponse, error) {
	var out listCurationResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	rules, err := h.DB.ListCurationRules(c.Request().Context(), owner)
	if err != nil {
		return out, apierr.Generic()
	}
	return listCurationResponse{Rules: rules}, nil
}

// createCurationResponse is POST /api/v1/users/current/curation.
type createCurationResponse struct {
	Rule *db.CurationRule `json:"rule"`
}

// CreateCuration: POST /api/v1/users/current/curation → {rule:CurationRule}.
// Validates axis (whitelist) + action, creates the rule, and applies it
// immediately (rename → backfill + rollup rebuild; hide → store only).
func (h *Handler) CreateCuration(c *echo.Context, req curationRequest) (createCurationResponse, error) {
	var out createCurationResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}

	// axis must be in the Heartbeats Explorer whitelist.
	if _, ok := db.ExploreColumn(req.Axis); !ok {
		return out, apierr.New(http.StatusBadRequest, "Unknown axis: "+req.Axis, nil)
	}
	// A pin (canonical entities) is an additive third action: it stores a value
	// that a grouped query always keeps as its own slice (never "Other"). It
	// carries no target (like hide) and only feeds the query-time bucket policy
	// — the rename-only guards below (newValue, template, apply_at_ingest) skip
	// it, and it stores as match_type "exact" with a null new_value.
	if req.Action != db.CurationHide && req.Action != db.CurationRename && req.Action != db.CurationPin {
		return out, apierr.New(http.StatusBadRequest, "action must be 'hide', 'rename', or 'pin'", nil)
	}
	if req.MatchValue == "" {
		return out, apierr.New(http.StatusBadRequest, "matchValue is required", nil)
	}
	matchType := req.MatchType
	if matchType == "" {
		matchType = db.MatchExact
	}
	if matchType != db.MatchExact && matchType != db.MatchRegex && matchType != db.MatchTemplate {
		return out, apierr.New(http.StatusBadRequest, "matchType must be 'exact', 'regex', or 'template'", nil)
	}
	// A template rule's target is a capture-group replacement template — it only
	// makes sense for rename (hide has no target).
	if matchType == db.MatchTemplate && req.Action != db.CurationRename {
		return out, apierr.New(http.StatusBadRequest, "matchType 'template' is only valid for a rename rule", nil)
	}
	newValue := req.NewValue
	if req.Action == db.CurationRename {
		if newValue == nil || *newValue == "" {
			return out, apierr.New(http.StatusBadRequest, "newValue is required for a rename rule", nil)
		}
		if req.Axis == "day" {
			return out, apierr.New(http.StatusBadRequest, "the day axis cannot be renamed", nil)
		}
		// Accept both Postgres `\1` and shell-style `$1` backrefs in a template;
		// normalize `$N` -> `\N` before storing/using so either works.
		if matchType == db.MatchTemplate {
			normalized := db.NormalizeTemplate(*newValue)
			newValue = &normalized
		}
	}

	ctx := c.Request().Context()
	// For a regex rule, validate the pattern compiles (Postgres regex) up front.
	if matchType == db.MatchRegex {
		if err := h.DB.ValidateRegex(ctx, req.MatchValue); err != nil {
			return out, apierr.New(http.StatusBadRequest, "invalid regex pattern", nil)
		}
	}
	// For a template rule, validate the pattern compiles AND the template is a
	// valid regexp_replace replacement (guards bad backrefs like `\9`).
	if matchType == db.MatchTemplate {
		if err := h.DB.ValidateTemplate(ctx, req.MatchValue, *newValue); err != nil {
			return out, apierr.New(http.StatusBadRequest, "invalid template rename", nil)
		}
	}

	// boom-scrub: apply_at_ingest is a rename-only flag (a hide rule has nothing
	// to rewrite). Because the ingest applier uses Go RE2 — stricter than the
	// Postgres regex the checks above use (no pattern backrefs / lookaround) —
	// validate the pattern compiles under Go too, else the rule would save but
	// silently no-op at ingest.
	if req.ApplyAtIngest {
		if req.Action != db.CurationRename {
			return out, apierr.New(http.StatusBadRequest, "apply at ingest is only valid for a rename rule", nil)
		}
		if err := db.ValidateIngestRenamePattern(matchType, req.MatchValue); err != nil {
			return out, apierr.New(http.StatusBadRequest, "pattern does not compile for ingest apply: "+err.Error(), nil)
		}
	}

	// Both hide and rename are stored as rules. A plain rule is applied at QUERY
	// TIME (non-destructive, reversible remap). A rename flagged apply_at_ingest
	// ALSO rewrites newly-ingested rows (and is excluded from query-time remap so
	// it doesn't double-apply — see remap.go / rename_apply.go).
	rule, err := h.DB.CreateCurationRuleWithIngest(ctx, owner, req.Axis, req.Action, matchType, req.MatchValue, newValue, req.ApplyAtIngest)
	if err != nil {
		h.Logger.Error("create curation rule failed", "err", err)
		return out, apierr.Generic()
	}

	// Both hide and rename change what dashboards show → drop this user's cached
	// aggregations so the new rule takes effect immediately.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)

	return createCurationResponse{Rule: rule}, nil
}

// DeleteCuration: DELETE /api/v1/users/current/curation/:id → 204.
// Both hide and rename are query-time and fully reversible: deleting a hide rule
// un-hides, and deleting a rename rule instantly reverts the dashboards to the
// raw (un-merged) values (raw records were never mutated).
func (h *Handler) DeleteCuration(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apierr.New(http.StatusBadRequest, "Invalid rule id", nil)
	}
	n, err := h.DB.DeleteCurationRule(c.Request().Context(), owner, id)
	if err != nil {
		return apierr.Generic()
	}
	if n == 0 {
		return apierr.New(http.StatusNotFound, "Curation rule not found", nil)
	}
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	// 204 is written by the apiroute seam (apiroute.NoContent).
	return nil
}

// toggleCurationRequest is the optional POST body for the toggle endpoint.
// When Enabled is nil the current value is flipped; when non-nil the exact
// value is written (idempotent — no-op if already at the requested value).
//
// Body is optional — an empty POST flips. When present it must be tiny (a
// single boolean); the apiroute seam binds it under the small body cap, and
// a zero-length body binds to the nil-Enabled zero value rather than erroring.
type toggleCurationRequest struct {
	Enabled *bool `json:"enabled"`
}

// toggleCurationResponse is POST /api/v1/users/current/curation/:id/toggle.
type toggleCurationResponse struct {
	Enabled bool `json:"enabled"`
}

// ToggleCuration: POST /api/v1/users/current/curation/:id/toggle → {enabled:bool}.
// boom-dfd. Pauses / resumes a curation rule without deleting it. Owner-
// scoped. Body is optional: omit to flip, or pass {"enabled":true|false} to
// set an exact state. Both flip and set are idempotent — sending the same
// state twice still returns 200 with the current value.
//
// A disabled rule stays in ListCurationRules (so the UI can surface it) but
// is filtered out of LoadHiddenSets / LoadRenameSets — its effect is
// paused. Apply and Purge reject disabled rules with 400 (see below).
func (h *Handler) ToggleCuration(c *echo.Context, req toggleCurationRequest) (toggleCurationResponse, error) {
	var out toggleCurationResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return out, apierr.New(http.StatusBadRequest, "Invalid rule id", nil)
	}
	ctx := c.Request().Context()
	var newEnabled bool
	var found bool
	if req.Enabled != nil {
		found, err = h.DB.SetCurationRuleEnabled(ctx, owner, id, *req.Enabled)
		newEnabled = *req.Enabled
	} else {
		newEnabled, found, err = h.DB.ToggleCurationRule(ctx, owner, id)
	}
	if err != nil {
		h.Logger.Error("toggle curation rule failed", "err", err, "ruleId", id)
		return out, apierr.Generic()
	}
	if !found {
		return out, apierr.New(http.StatusNotFound, "Curation rule not found", nil)
	}
	// Enabling/disabling a rule changes what dashboards render → drop the
	// owner's cached aggregations so the next fetch reflects the new state.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
	return toggleCurationResponse{Enabled: newEnabled}, nil
}

// CurationAffected: GET /api/v1/users/current/curation/:id/affected →
// {values:[{value,count}], truncated}. The DISTINCT RAW values (with heartbeat
// counts) a rule matches on its axis — the one literal for an exact rule, every
// matching value for a regex rule. Owner-scoped, UNFILTERED (audit).
// curationAffectedResponse is GET /api/v1/users/current/curation/:id/affected.
type curationAffectedResponse struct {
	// Values are the DISTINCT RAW values (with heartbeat counts) the rule
	// matches on its axis — capped at 200 by the caller below.
	Values []db.AffectedValue `json:"values"`
	// Truncated reports that the cap clipped the list.
	Truncated bool `json:"truncated"`
}

func (h *Handler) CurationAffected(c *echo.Context) (curationAffectedResponse, error) {
	var out curationAffectedResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return out, apierr.New(http.StatusBadRequest, "Invalid rule id", nil)
	}
	ctx := c.Request().Context()

	rule, ruleOwner, err := h.DB.GetCurationRule(ctx, id)
	if err != nil {
		return out, apierr.Generic()
	}
	if rule == nil || ruleOwner != owner {
		return out, apierr.New(http.StatusNotFound, "Curation rule not found", nil)
	}

	values, truncated, err := h.DB.CurationAffectedValues(ctx, owner, rule, 200)
	if err != nil {
		h.Logger.Error("curation affected values failed", "err", err)
		return out, apierr.Generic()
	}
	return curationAffectedResponse{Values: values, Truncated: truncated}, nil
}

// applyPreviewRowsCap is the max number of before/after rows returned by the
// apply preview. The DB touches all rows on apply — this is only the modal
// display cap (matches the frontend "and N more…" footer).
const applyPreviewRowsCap = 100

// resolveCurationRule fetches the rule and enforces owner scoping. Returns a
// 404-wrapped apierr when the rule is missing OR owned by someone else (the
// two are indistinguishable to the caller — deliberate: never leak that
// another user's rule exists). Shared by every destructive-action handler.
func (h *Handler) resolveCurationRule(c *echo.Context, ctx context.Context, owner string, id int) (*db.CurationRule, *apierr.Error) {
	rule, ruleOwner, err := h.DB.GetCurationRule(ctx, id)
	if err != nil {
		return nil, apierr.Generic()
	}
	if rule == nil || ruleOwner != owner {
		return nil, apierr.New(http.StatusNotFound, "Curation rule not found", nil)
	}
	return rule, nil
}

// --- preview payload, DECLARED for the spec only -----------------------------
//
// ApplyRenamePreview writes its own JSON because it answers two DIFFERENT
// shapes discriminated on `action`, and one Go struct cannot be both. The types
// below are therefore registered via apiroute.WritesJSON: they DECLARE the
// payload for the OpenAPI schema without taking over the encoding, so the bytes
// on the wire are still the maps the handler builds below. That is deliberate —
// making the handler return a struct would put `omitempty` in charge of the
// wire and silently drop a legitimately-empty `before`/`after` value.
//
// The declaration is a SUPERSET of the two branches: every field a branch does
// not emit is optional, so both real payloads validate against it. It is not a
// strict oneOf; the seam reflects exactly one Go type per route.
//
// If you change what the handler writes, change these tags in the same commit —
// nothing enforces the pairing.

// curationPreviewRule is the `rule` block echoed identically on both branches
// so the FE only has to switch on `action`. A subset of db.CurationRule —
// enabled / applyAtIngest / createdAt are deliberately not echoed.
type curationPreviewRule struct {
	ID         int     `json:"id"`
	Axis       string  `json:"axis"`
	Action     string  `json:"action"`
	MatchType  string  `json:"matchType"`
	MatchValue string  `json:"matchValue"`
	NewValue   *string `json:"newValue"`
}

// curationPreviewRow is one entry of `affectedRows`. The rename branch emits
// db.AffectedRowDiff ({id, before, after}); the hide branch emits
// db.PurgeRowDiff ({id, deleted}). `id` is the only field common to both.
type curationPreviewRow struct {
	ID int64 `json:"id"`
	// Before / After are the rename branch only: the value on the target
	// column now and after the UPDATE.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Deleted is the hide branch only: the raw column values on the row that
	// the DELETE would remove, keyed by column.
	Deleted map[string]string `json:"deleted,omitempty"`
}

// curationPreviewResponse is GET /api/v1/users/current/curation/:id/preview.
type curationPreviewResponse struct {
	// Action is the discriminator: "rename" or "hide".
	Action string `json:"action"`
	// SQLPlanned is both statements of the branch, semicolon-joined — what
	// /apply or /purge would run verbatim.
	SQLPlanned string `json:"sqlPlanned"`
	// SQLUpdate / SQLDelete are the rename branch only.
	SQLUpdate string `json:"sqlUpdate,omitempty"`
	SQLDelete string `json:"sqlDelete,omitempty"`
	// SQLDeleteRows / SQLDeleteRule are the hide branch only.
	SQLDeleteRows string `json:"sqlDeleteRows,omitempty"`
	SQLDeleteRule string `json:"sqlDeleteRule,omitempty"`
	// AffectedRows is capped at applyPreviewRowsCap (100) rows.
	AffectedRows []curationPreviewRow `json:"affectedRows"`
	// TotalAffected is the EXACT count, uncapped — the modal renders
	// "and N more…" from totalAffected - rowsShown.
	TotalAffected int64 `json:"totalAffected"`
	// RowsShown is len(affectedRows).
	RowsShown int                 `json:"rowsShown"`
	Rule      curationPreviewRule `json:"rule"`
}

// ApplyRenamePreview: GET /api/v1/users/current/curation/:id/preview.
// Dispatches on rule.action — a rename rule preview returns the apply-shaped
// payload (UPDATE + rule-delete SQL, before/after per row), a hide rule
// preview returns the purge-shaped payload (DELETE heartbeats + rule-delete
// SQL, per-row "will be deleted" info). Owner-scoped; no data is mutated.
// One preview endpoint, two payload shapes — the FE modal dispatches on the
// same `action` discriminator to render the appropriate UI.
func (h *Handler) ApplyRenamePreview(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "Invalid rule id", nil))
	}
	ctx := c.Request().Context()

	rule, aerr := h.resolveCurationRule(c, ctx, owner, id)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}

	// Rule shape returned on both branches — kept identical so the FE
	// discriminator (`action`) is the only per-variant switch it needs.
	ruleOut := map[string]any{
		"id":         rule.ID,
		"axis":       rule.Axis,
		"action":     rule.Action,
		"matchType":  rule.MatchType,
		"matchValue": rule.MatchValue,
		"newValue":   rule.NewValue,
	}

	switch rule.Action {
	case db.CurationRename:
		updSQL, delSQL, diff, total, perr := h.DB.ApplyRenamePreview(ctx, owner, rule, applyPreviewRowsCap)
		if perr != nil {
			h.Logger.Error("apply-rename preview failed", "err", perr, "ruleId", id)
			return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, perr.Error(), nil))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"action":        "rename",
			"sqlPlanned":    updSQL + ";\n" + delSQL + ";",
			"sqlUpdate":     updSQL,
			"sqlDelete":     delSQL,
			"affectedRows":  diff,
			"totalAffected": total,
			"rowsShown":     len(diff),
			"rule":          ruleOut,
		})
	case db.CurationHide:
		delRowsSQL, delRuleSQL, diff, total, perr := h.DB.PurgeHiddenPreview(ctx, owner, rule, applyPreviewRowsCap)
		if perr != nil {
			h.Logger.Error("purge-hidden preview failed", "err", perr, "ruleId", id)
			return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, perr.Error(), nil))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"action":        "hide",
			"sqlPlanned":    delRowsSQL + ";\n" + delRuleSQL + ";",
			"sqlDeleteRows": delRowsSQL,
			"sqlDeleteRule": delRuleSQL,
			"affectedRows":  diff,
			"totalAffected": total,
			"rowsShown":     len(diff),
			"rule":          ruleOut,
		})
	default:
		return apihelpers.RespondErr(c, apierr.New(http.StatusBadRequest, "unknown rule action: "+rule.Action, nil))
	}
}

// applyRenameResponse is POST /api/v1/users/current/curation/:id/apply.
type applyRenameResponse struct {
	RowsAffected int64 `json:"rowsAffected"`
	// SQLRun is the UPDATE + rule-DELETE pair, semicolon-joined, exactly as
	// executed — the modal renders it verbatim.
	SQLRun    string `json:"sqlRun"`
	SQLUpdate string `json:"sqlUpdate"`
	SQLDelete string `json:"sqlDelete"`
}

// ApplyRename: POST /api/v1/users/current/curation/:id/apply →
// {rowsAffected:int, sqlRun:string}. DESTRUCTIVELY rewrites every heartbeat
// row that the rename rule matches on the target column, then removes the
// rule row itself, ATOMICALLY in one transaction. Owner-scoped. Rejects
// non-rename rules with 400 (hide rules go through /purge instead — a
// destructive-delete UX is scarier and gets its own path).
//
// Idempotent-in-effect: if the mapping is already applied and 0 rows match,
// still succeeds with rowsAffected=0 and the rule row is still removed.
func (h *Handler) ApplyRename(c *echo.Context) (applyRenameResponse, error) {
	var out applyRenameResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return out, apierr.New(http.StatusBadRequest, "Invalid rule id", nil)
	}
	ctx := c.Request().Context()

	rule, aerr := h.resolveCurationRule(c, ctx, owner, id)
	if aerr != nil {
		return out, aerr
	}
	if rule.Action != db.CurationRename {
		return out, apierr.New(http.StatusBadRequest, "only rename rules can be applied", nil)
	}
	// boom-dfd: refuse to run a destructive action against a paused rule —
	// applying-a-rule-you-just-paused is confusing and probably a mistake.
	// The user should re-enable, verify it still matches what they expect,
	// and then apply.
	if !rule.Enabled {
		return out, apierr.New(http.StatusBadRequest, "cannot apply a disabled rule; enable it first", nil)
	}

	rows, sqlUpd, sqlDel, err := h.DB.ApplyRenameRule(ctx, owner, rule)
	if err != nil {
		h.Logger.Error("apply-rename failed", "err", err, "ruleId", id)
		return out, apierr.Generic()
	}

	// The apply mutated raw heartbeats and removed a rule → dashboards, the
	// explorer, and per-axis values all change. Drop the owner's cached
	// aggregations so the next fetch is fresh.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)

	h.Logger.Info("curation rename applied", "ruleId", id, "rows", rows)
	return applyRenameResponse{
		RowsAffected: rows,
		SQLRun:       sqlUpd + ";\n" + sqlDel + ";",
		SQLUpdate:    sqlUpd,
		SQLDelete:    sqlDel,
	}, nil
}

// purgeHiddenResponse is POST /api/v1/users/current/curation/:id/purge.
type purgeHiddenResponse struct {
	RowsAffected int64 `json:"rowsAffected"`
	// SQLRun is the heartbeats-DELETE + rule-DELETE pair, semicolon-joined,
	// exactly as executed — the modal renders it verbatim.
	SQLRun        string `json:"sqlRun"`
	SQLDeleteRows string `json:"sqlDeleteRows"`
	SQLDeleteRule string `json:"sqlDeleteRule"`
}

// PurgeHidden: POST /api/v1/users/current/curation/:id/purge →
// {rowsAffected:int, sqlRun:string, sqlDeleteRows:string, sqlDeleteRule:string}.
// DESTRUCTIVELY deletes every heartbeat row a HIDE rule matches, then removes
// the rule itself, ATOMICALLY in one transaction. Owner-scoped. Rejects
// non-hide rules with 400 — rename rules go through /apply, which preserves
// raw data under a rewrite (this endpoint destroys raw data). Idempotent-in-
// effect: 0 matches still deletes the rule and returns rowsAffected=0.
//
// This is the scariest endpoint in the curation family — the FE modal MUST
// gate it behind a "type rule id N to confirm" input, and the icon in the
// row list gets a redder destructive tint than /apply's Zap.
func (h *Handler) PurgeHidden(c *echo.Context) (purgeHiddenResponse, error) {
	var out purgeHiddenResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return out, apierr.New(http.StatusBadRequest, "Invalid rule id", nil)
	}
	ctx := c.Request().Context()

	rule, aerr := h.resolveCurationRule(c, ctx, owner, id)
	if aerr != nil {
		return out, aerr
	}
	if rule.Action != db.CurationHide {
		return out, apierr.New(http.StatusBadRequest, "only hide rules can be purged", nil)
	}
	// boom-dfd: refuse to purge against a paused rule — the same reasoning
	// as the apply guard, and purge is the more dangerous of the two.
	if !rule.Enabled {
		return out, apierr.New(http.StatusBadRequest, "cannot purge a disabled rule; enable it first", nil)
	}

	rows, sqlDelRows, sqlDelRule, err := h.DB.PurgeHiddenRule(ctx, owner, rule)
	if err != nil {
		h.Logger.Error("purge-hidden failed", "err", err, "ruleId", id)
		return out, apierr.Generic()
	}

	// Purge deleted raw heartbeats + removed a rule → dashboards, the
	// explorer, and per-axis values all change. Drop the owner's cached
	// aggregations so the next fetch is fresh.
	apihelpers.InvalidateOwnerCache(h.Cache, owner)

	h.Logger.Info("curation purge-hidden", "ruleId", id, "rows", rows)
	return purgeHiddenResponse{
		RowsAffected:  rows,
		SQLRun:        sqlDelRows + ";\n" + sqlDelRule + ";",
		SQLDeleteRows: sqlDelRows,
		SQLDeleteRule: sqlDelRule,
	}, nil
}
