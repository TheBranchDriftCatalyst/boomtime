package admin

// users.go — the admin caps dashboard data source (boom-93f.6). Surfaces every
// user's role/tier + effective capabilities + disabled status, plus the
// role→capabilities legend, so an operator can see who's on which tier and what
// each tier grants. Admin-gated. Read-only v1 (set-role/disable stay in the
// `boomtime user` CLI for now).
import (
	"fmt"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/labstack/echo/v5"
)

// adminUserRow is one user in the caps dashboard.
type adminUserRow struct {
	Username     string          `json:"username"`
	Role         string          `json:"role"`
	Disabled     bool            `json:"disabled"`
	Capabilities map[string]bool `json:"capabilities"` // EFFECTIVE (role defaults + overrides)
}

// adminUsersResponse is GET /api/v1/admin/users.
type adminUsersResponse struct {
	// Capabilities is the canonical column order for the table + legend.
	Capabilities []string `json:"capabilities"`
	// Roles is the role→default-capabilities legend ("what each tier grants").
	Roles map[string]map[string]bool `json:"roles"`
	// Users is every account with its effective grants.
	Users []adminUserRow `json:"users"`
}

// ListUsers: GET /api/v1/admin/users (admin-gated).
func (h *Handler) ListUsers(c *echo.Context) (adminUsersResponse, error) {
	var out adminUsersResponse
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return out, aerr
	}
	rows, err := h.DB.ListUsersAdmin(c.Request().Context())
	if err != nil {
		return out, fmt.Errorf("admin list users failed: %w", err)
	}
	users := make([]adminUserRow, 0, len(rows))
	for _, r := range rows {
		disabled := r.DisabledAt != nil
		// Effective caps = role defaults merged with the per-user override
		// blob, disabled short-circuiting to all-false.
		ident := auth.BuildIdentity(r.Username, r.Role, r.Capabilities, disabled)
		users = append(users, adminUserRow{
			Username:     r.Username,
			Role:         r.Role,
			Disabled:     disabled,
			Capabilities: ident.Capabilities(),
		})
	}
	return adminUsersResponse{
		Capabilities: auth.CapabilityStrings(),
		Roles:        auth.RoleCapabilityMatrix(),
		Users:        users,
	}, nil
}
