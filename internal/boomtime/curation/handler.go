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
// under a bounded MaxBytesReader cap (apihelpers.BodyLimitMedium — 64 KiB — for
// create/update; apihelpers.BodyLimitSmall — 4 KiB — for the toggle boolean).
// Admin-gated endpoints (labels CRUD, gen-config, seed.sql) go through
// h.requireAdmin BEFORE reading the body so a non-admin request never
// costs a body allocation. Destructive paths (/apply, /purge) reject a
// disabled rule with 400 so accidentally-applying a paused rule stays
// impossible — gaka-dfd guard preserved verbatim.
//
// Shared helpers live in internal/apihelpers/ — this package imports
// that instead of carrying per-file shims (gaka-8tn phase 8 collapse).
package curation

import (
	"log/slog"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/cache"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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

// requireAdmin: 401 without a token, 403 when not on the admin allowlist.
// Returns the resolved owner on success. Mirror of the same method on
// *admin.Handler / *identity.Handler — the labels admin endpoints gate
// on it. Three byte-identical copies survive because each domain guards a
// distinct endpoint and a shared helper would need dependency-injection
// scaffolding bigger than the 8-line body itself.
//
// The 403 path deliberately does NOT distinguish "unknown admin config"
// from "not on the list" — both look like a plain 403 to the client.
func (h *Handler) requireAdmin(c *echo.Context) (string, *apierr.Error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return "", aerr
	}
	if !h.Cfg.IsAdmin(owner) {
		return "", apierr.New(http.StatusForbidden, "admin only", nil)
	}
	return owner, nil
}
