// capability.go — the role + capability primitives for the user-demarcation
// substrate (boom-0oe / boom-93f). Pure types + the role→default-grant map;
// no DB, no config, no HTTP. The Identity that consumes these lives in
// identity.go.
//
// Design intent (docs/design/user-model-and-oidc.md §4): a coarse Role sets a
// user's default capability grants; a per-user JSONB `capabilities` override
// blob can flip individual grants. Roles are TEXT (not a PG enum) so adding a
// tier stays a one-line Go change. Everything here is inert until
// BOOM_FEATURE_USER_MODEL flips on — see apihelpers.Identify.
//
// This package is deliberately dependency-light so it can later lift out into
// a reusable `catalyst-auth` module (boom-93f architecture note) without
// dragging boomtime internals along.
package auth

// Role is a user's coarse tier. Stored in users.role (TEXT, default 'full').
type Role string

const (
	// RoleFull is today's every-user default: full dashboards, ingest,
	// import, backup, rollup generation. Existing rows land here on migrate.
	RoleFull Role = "full"
	// RoleLight is the cheap read-mostly tier (free/evaluator/public-only):
	// can read dashboards + curate, but NOT ingest/import/backup and its
	// heartbeats skip the expensive rollup machinery.
	RoleLight Role = "light"
	// RoleService is a write-only automation identity (e.g. a CI job pushing
	// heartbeats): may ingest, but has no dashboard/curation surface and
	// generates no rollups.
	RoleService Role = "service"
	// RoleAdmin is a superuser: every capability including cross-user admin.
	RoleAdmin Role = "admin"
)

// AllRoles is the canonical role set, in tier order. Used by ValidRole and by
// the CLI's shell-completion for `boomtime user set-role`.
var AllRoles = []Role{RoleFull, RoleLight, RoleService, RoleAdmin}

// RoleStrings returns AllRoles as plain strings (for cobra ValidArgs /
// completion, which speak []string).
func RoleStrings() []string {
	out := make([]string, len(AllRoles))
	for i, r := range AllRoles {
		out[i] = string(r)
	}
	return out
}

// ValidRole reports whether r names a known role.
func ValidRole(r string) bool {
	for _, k := range AllRoles {
		if Role(r) == k {
			return true
		}
	}
	return false
}

// Capability is a single gated ability. Handlers gate on ident.Can(<cap>).
// The string values are the keys used in the users.capabilities JSONB override
// blob (so an operator can flip one grant without changing the role).
type Capability string

const (
	CapReadDashboards   Capability = "read_dashboards"
	CapCurate           Capability = "curate"
	CapIngestHeartbeats Capability = "ingest_heartbeats"
	CapImport           Capability = "import"
	CapBackup           Capability = "backup"
	CapGenerateRollups  Capability = "generate_rollups"
	CapAdmin            Capability = "admin"
)

// AllCapabilities is the full set, used to build the flag-off all-true
// Identity and to validate override keys.
var AllCapabilities = []Capability{
	CapReadDashboards,
	CapCurate,
	CapIngestHeartbeats,
	CapImport,
	CapBackup,
	CapGenerateRollups,
	CapAdmin,
}

// RoleFromGroups derives a boomtime Role from an OIDC identity's provider
// groups via the BOOM_AUTHENTIK_GROUP_TO_ROLE map (boom-0oe.11). Among all
// groups that map to a valid role, the HIGHEST-privilege one wins
// (admin>full>service>light) — deterministic regardless of token group order.
// No match → RoleLight (fail-closed to the cheapest tier).
func RoleFromGroups(groups []string, groupToRole map[string]string) Role {
	best, bestPriority := RoleLight, -1
	for _, g := range groups {
		r, ok := groupToRole[g]
		if !ok || !ValidRole(r) {
			continue
		}
		if p := rolePriority(Role(r)); p > bestPriority {
			best, bestPriority = Role(r), p
		}
	}
	return best
}

func rolePriority(r Role) int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleFull:
		return 2
	case RoleService:
		return 1
	default: // RoleLight + unknown
		return 0
	}
}

// CapabilityStrings returns AllCapabilities as plain strings, in declaration
// order — the canonical column order for the admin caps dashboard.
func CapabilityStrings() []string {
	out := make([]string, len(AllCapabilities))
	for i, c := range AllCapabilities {
		out[i] = string(c)
	}
	return out
}

// RoleCapabilityMatrix returns each role's DEFAULT capability grants as
// strings — the legend the admin UI renders ("what does each tier grant").
// A deep copy, so callers can't mutate roleDefaults.
func RoleCapabilityMatrix() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(roleDefaults))
	for _, r := range AllRoles {
		caps := roleDefaults[r]
		m := make(map[string]bool, len(caps))
		for c, v := range caps {
			m[string(c)] = v
		}
		out[string(r)] = m
	}
	return out
}

// KnownCapability reports whether c is a recognized capability key. Override
// blobs may carry non-capability keys (e.g. storage_quota_bytes for the future
// quota bead) which are simply ignored by the capability gate.
func KnownCapability(c Capability) bool {
	for _, k := range AllCapabilities {
		if k == c {
			return true
		}
	}
	return false
}

// roleDefaults maps each role to its default capability grants. Every role
// lists every capability explicitly (no implicit false) so a new capability
// can't silently default-grant on an existing tier — the compiler + tests
// force a decision per role.
var roleDefaults = map[Role]map[Capability]bool{
	RoleFull: {
		CapReadDashboards:   true,
		CapCurate:           true,
		CapIngestHeartbeats: true,
		CapImport:           true,
		CapBackup:           true,
		CapGenerateRollups:  true,
		CapAdmin:            false,
	},
	RoleAdmin: {
		CapReadDashboards:   true,
		CapCurate:           true,
		CapIngestHeartbeats: true,
		CapImport:           true,
		CapBackup:           true,
		CapGenerateRollups:  true,
		CapAdmin:            true,
	},
	RoleLight: {
		CapReadDashboards:   true,
		CapCurate:           true,
		CapIngestHeartbeats: false,
		CapImport:           false,
		CapBackup:           false,
		CapGenerateRollups:  false,
		CapAdmin:            false,
	},
	RoleService: {
		CapReadDashboards:   false,
		CapCurate:           false,
		CapIngestHeartbeats: true,
		CapImport:           false,
		CapBackup:           false,
		CapGenerateRollups:  false,
		CapAdmin:            false,
	},
}

// defaultsForRole returns a COPY of the role's default grants, falling back to
// RoleLight (the cheapest, fail-closed tier) for an unknown role.
func defaultsForRole(r Role) (Role, map[Capability]bool) {
	base, ok := roleDefaults[r]
	if !ok {
		r = RoleLight
		base = roleDefaults[RoleLight]
	}
	out := make(map[Capability]bool, len(base))
	for c, v := range base {
		out[c] = v
	}
	return r, out
}
