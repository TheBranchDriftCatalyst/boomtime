// Package curation owns the HTTP surface for the data-curation domain:
// user-scoped hide/rename rules (list/create/delete/toggle/affected +
// the destructive preview/apply/purge triplet) AND the operator-facing
// labels catalog admin (public GET + admin CRUD + gen-config PATCH +
// seed.sql dumper).
//
// Extracted from internal/handler/ as part of gaka-8tn phase 5b. Domain
// scope covers ONLY the curation write/read surface and the labels
// catalog admin surface. Anything that reads LoadHiddenSets/LoadRenameSets
// downstream (dashboards, projects, awards, widgets, identity, spaces)
// stays out of this package — those callers keep depending on internal/db
// directly, same identity/awards/ingest precedent.
//
// SECURITY POSTURE: every user-scoped write endpoint here binds JSON
// under a bounded MaxBytesReader cap (BodyLimitMedium — 64 KiB — for
// create/update; BodyLimitSmall — 4 KiB — for the toggle boolean).
// Admin-gated endpoints (labels CRUD, gen-config, seed.sql) go through
// h.requireAdmin BEFORE reading the body so a non-admin request never
// costs a body allocation. Destructive paths (/apply, /purge) reject a
// disabled rule with 400 so accidentally-applying a paused rule stays
// impossible — gaka-dfd guard preserved verbatim.
//
// DB QUERIES STAY IN internal/db/: the receiver methods this package
// calls remain on *db.DB because they either have non-curation callers
// (LoadHiddenSets / LoadRenameSets are called from awards, identity,
// widgets, spaces, handler-side stats/projects; ExploreColumn / Match*
// / Curation* constants are shared with spaces; ListLabels / GetLabel /
// DeleteLabelImage are called from admin_label_images, worker) or
// share unexported helpers with cross-package callers. Only handlers
// move here in phase 5b — the DB slice defers to phase 8 collapse,
// mirroring the identity phase 4a / ingest phase 5a precedents.
//
// Shared helpers live in internal/apihelpers/ — this package imports
// that instead of carrying per-file shims.
package curation

import (
	"log/slog"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// Handler bundles the SUBSET of the god-type handler.Handler's
// dependencies that the curation domain actually reads. Everything else
// stays out of this package.
//
//   - DB     — every curation_rules read + write, plus labels catalog
//     (list/get/upsert/delete) and the label-images cascade on delete
//   - Cfg    — IsAdmin allowlist gate on the labels admin endpoints
//   - Logger — create/toggle/apply/purge failure log lines, label
//     upsert/delete failure logs, image-cascade warn line
//   - Cache  — busted on every curation write path (create, delete,
//     toggle, apply, purge) so dashboards + widgets pick up the new
//     rule state on the next fetch
type Handler struct {
	DB     *db.DB
	Cfg    *config.Config
	Logger *slog.Logger
	Cache  *cache.TTL
}

// New constructs a curation.Handler with the passed-in shared deps.
// Every field is required in production; nil-checks are the caller's
// responsibility (the god-type's New wires all four unconditionally).
func New(database *db.DB, cfg *config.Config, logger *slog.Logger, cch *cache.TTL) *Handler {
	return &Handler{
		DB:     database,
		Cfg:    cfg,
		Logger: logger,
		Cache:  cch,
	}
}

// resolveUser is the curation-domain adapter over apihelpers.ResolveUser
// — receiver-shaped so the extracted handlers keep their previous
// signature (`h.resolveUser(c)`) unchanged. Every call is line-identical
// to the god-type version.
func (h *Handler) resolveUser(c *echo.Context) (string, string, *apierr.Error) {
	return apihelpers.ResolveUser(h.DB, c)
}

// internalErr is the curation-domain adapter over apihelpers.InternalErr
// — receiver-shaped so per-handler call sites stay identical.
func (h *Handler) internalErr(c *echo.Context, msg string, err error) error {
	return apihelpers.InternalErr(h.Logger, c, msg, err)
}

// invalidateOwnerCache is the curation-domain adapter over
// apihelpers.InvalidateOwnerCache — receiver-shaped so curation.go
// call sites stay identical.
func (h *Handler) invalidateOwnerCache(owner string) {
	apihelpers.InvalidateOwnerCache(h.Cache, owner)
}

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *handler.Handler (defined in internal/handler/admin_label_images.go)
// and *identity.Handler — duplicated here because the labels admin
// endpoints gate on it and the admin domain is a phase-7 extraction.
// The three definitions stay byte-identical until phase 8 collapses
// them into internal/apihelpers.
//
// The 403 path deliberately does NOT distinguish "unknown admin config"
// from "not on the list" — both look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}

// respondErr renders an apierr.Error onto the context. Package-local
// alias for apihelpers.RespondErr so the extracted handler files keep
// their existing `respondErr(c, ...)` call sites unchanged.
func respondErr(c *echo.Context, e *apierr.Error) error {
	return apihelpers.RespondErr(c, e)
}

// noContent renders a 204 (PostNoContent / DeleteNoContent). Package-
// local alias so curation.go's `noContent(c)` call site stays identical.
func noContent(c *echo.Context) error {
	return c.NoContent(http.StatusNoContent)
}

// BindJSONWithLimit / body-size limits: curation re-exports the shared
// helpers under package-local aliases so the extracted files keep their
// original call sites (`BindJSONWithLimit(c, &req, BodyLimitMedium)`).
// These are the SAME buckets defined in apihelpers — the aliases keep
// call-site diffs to zero.

// BodyLimitSmall / BodyLimitMedium / BodyLimitLarge: package-local
// aliases over apihelpers so curation handlers keep their pre-refactor
// call sites. Delete these once phase 8 collapses call sites to the
// apihelpers-qualified form.
const (
	BodyLimitSmall  = apihelpers.BodyLimitSmall
	BodyLimitMedium = apihelpers.BodyLimitMedium
	BodyLimitLarge  = apihelpers.BodyLimitLarge
)

// BindJSONWithLimit: package-local alias for apihelpers.BindJSONWithLimit.
func BindJSONWithLimit(c *echo.Context, dst any, limit int64) *apierr.Error {
	return apihelpers.BindJSONWithLimit(c, dst, limit)
}
