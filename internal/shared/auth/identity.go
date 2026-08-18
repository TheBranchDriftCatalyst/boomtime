// identity.go — the Identity value that handlers gate on (gaka-0oe / gaka-93f).
//
// An Identity is the resolved caller: a username + its effective capability
// set + a disabled flag. Handlers ask ident.Can(<cap>) instead of reasoning
// about role strings. Two constructors:
//
//   - AllCapsIdentity: the flag-OFF identity (every capability granted). This
//     is what apihelpers.Identify returns when BOOM_FEATURE_USER_MODEL is off,
//     so no gate ever fires and behavior is byte-identical to pre-substrate.
//   - BuildIdentity: the flag-ON identity, computed from (role, capabilities
//     JSONB override, disabled) read off the users row.
package auth

import "encoding/json"

// Identity is a resolved caller with its effective capabilities. The
// capability map is unexported so callers must go through Can() (which also
// enforces the disabled short-circuit).
type Identity struct {
	Username string
	Role     Role
	Disabled bool
	caps     map[Capability]bool
}

// Can reports whether the identity is granted capability c. A disabled
// identity can do nothing (fail-closed), regardless of role/overrides.
func (i *Identity) Can(c Capability) bool {
	if i == nil || i.Disabled {
		return false
	}
	return i.caps[c]
}

// IsAdmin is sugar for Can(CapAdmin).
func (i *Identity) IsAdmin() bool { return i.Can(CapAdmin) }

// Capabilities returns a copy of the identity's EFFECTIVE capability grants
// (every AllCapabilities key → Can()), so a disabled identity reports all
// false. For admin/introspection surfaces (the caps dashboard); keys are
// strings for direct JSON encoding.
func (i *Identity) Capabilities() map[string]bool {
	out := make(map[string]bool, len(AllCapabilities))
	for _, c := range AllCapabilities {
		out[string(c)] = i.Can(c)
	}
	return out
}

// AllCapsIdentity builds the flag-off identity: role=full, every capability
// granted, never disabled. Preserves today's behavior when the user-model
// feature flag is off.
//
// SECURITY GUARDRAIL (gaka-93f.19): under BOOM_FEATURE_USER_MODEL=off this is
// what resolveIdentity returns for EVERY caller, so IsAdmin()/Can(CapAdmin) is
// true for EVERYONE. Capability gating is therefore inert with the flag off by
// design (byte-identical to pre-substrate). Consequently NO handler may gate an
// admin-only action on ident.IsAdmin() / ident.Can(CapAdmin) alone — with the
// flag off that check passes for all users. Admin authorization must stay on
// Cfg.IsAdmin / BOOM_ADMIN_USERS (the allowlist that is real regardless of the
// flag). Only combine an ident.CapAdmin check with an explicit
// UserModelEnabled() guard if you truly want admin gating that no-ops when the
// substrate is off.
func AllCapsIdentity(username string) *Identity {
	caps := make(map[Capability]bool, len(AllCapabilities))
	for _, c := range AllCapabilities {
		caps[c] = true
	}
	return &Identity{Username: username, Role: RoleFull, caps: caps}
}

// BuildIdentity computes the effective identity from the stored role, the raw
// users.capabilities JSONB override blob, and the disabled flag.
//
//   - Unknown role → RoleLight (fail-closed to the cheapest tier).
//   - Override blob: boolean values on known capability keys flip the role
//     default. Non-boolean values (e.g. a future storage_quota_bytes number)
//     and unknown keys are ignored by the capability gate.
//   - disabled=true yields an identity whose Can() always returns false.
func BuildIdentity(username, role string, capabilitiesJSON []byte, disabled bool) *Identity {
	effRole, eff := defaultsForRole(Role(role))

	if len(capabilitiesJSON) > 0 {
		var overrides map[string]any
		if err := json.Unmarshal(capabilitiesJSON, &overrides); err == nil {
			for k, v := range overrides {
				cap := Capability(k)
				if !KnownCapability(cap) {
					continue
				}
				if b, ok := v.(bool); ok {
					eff[cap] = b
				}
			}
		}
	}

	return &Identity{Username: username, Role: effRole, Disabled: disabled, caps: eff}
}
