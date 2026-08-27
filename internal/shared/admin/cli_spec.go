// cli_spec.go — GET /api/v1/admin/cli/spec (admin CLI-runner backend,
// BOOM_FEATURE_ADMIN_CLI). Serves the introspected CommandSpec for every
// command that is BOTH web-annotated AND registered in internal/climeta's
// vetted registry (and available under the current config). The FE renders
// this as the command palette; nothing outside the double allowlist is ever
// visible, let alone runnable.
//
// SECURITY: requireAdmin runs FIRST (the BOOM_ADMIN_USERS hard gate);
// RequireCap(CapAdmin) is attached as route middleware in routes.go
// (defense-in-depth — inert until BOOM_FEATURE_USER_MODEL is on). The routes
// themselves only exist when Cfg.FeatureAdminCLI is on.
package admin

import (
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
)

// cliSpecResponse is the GET /api/v1/admin/cli/spec envelope.
type cliSpecResponse struct {
	Commands []climeta.CommandSpec `json:"commands"`
}

// CLISpec returns the runnable-command catalog for the admin CLI-runner.
func (h *Handler) CLISpec(c *echo.Context) (cliSpecResponse, error) {
	var out cliSpecResponse
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return out, aerr
	}
	return cliSpecResponse{Commands: climeta.BuildSpecs(h.Cfg)}, nil
}
