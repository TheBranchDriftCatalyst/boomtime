// Meta endpoints: build/version disclosure + embedded changelog.
// Mirrors internal/handler/meta.go.

export interface VersionResponse {
  version: string;
}

// Public client-config advertisement — GET /api/v1/config/public (gaka-93f.1.1).
// Mirrors internal/meta/config_public.go PublicConfigResponse. Non-sensitive:
// only modes/flags the FE needs at boot to pick the auth + onboarding flow.
export interface PublicConfig {
  registration_enabled: boolean;
  auth_provider: string; // "local" | "oidc"
  oidc_enabled: boolean;
  billing_enabled: boolean;
  beta_flags: Record<string, boolean>; // e.g. { user_registration: true }
  // gaka-2ip Phase 1: per-user GitHub connect. true ONLY when the server gate
  // is on AND the OAuth-App creds + state signing key are configured. The
  // GitHubConnectCard renders nothing when false, so the surface is inert.
  github_connect_enabled: boolean;
  // Part B Stage 3 (gaka-174.x): mirrors BOOM_WIDGET_SPEC_ENGINE. When true,
  // WidgetRenderer.tsx / OverviewWidgetRenderer.tsx delegate target:"both"
  // kinds to the generic SpecRenderer instead of their bespoke switch cases.
  widget_spec_engine: boolean;
}

// Admin caps dashboard — GET /api/v1/admin/users (gaka-93f.6). Mirrors
// internal/admin/users.go adminUsersResponse.
export interface AdminUserRow {
  username: string;
  role: string;
  disabled: boolean;
  capabilities: Record<string, boolean>; // effective (role defaults + overrides)
}
export interface AdminUsersPayload {
  capabilities: string[]; // canonical column order
  roles: Record<string, Record<string, boolean>>; // role -> default caps (legend)
  users: AdminUserRow[];
}

// Linked external identities — GET /api/v1/users/current/identities (gaka-b5n.4).
export interface LinkedIdentity {
  provider: string;
  email: string;
  subPrefix: string;
  linkedAt: string;
}
export interface IdentitiesPayload {
  identities: LinkedIdentity[];
  oidcAvailable: boolean; // is OIDC configured (link possible)?
  hasPassword: boolean; // does the caller still have a local password?
}

// GitHub connection status — GET /api/v1/users/current/github (gaka-2ip Phase 1).
// Mirrors internal/identity/github_oauth.go githubConnectionResponse. NEVER
// carries the access token — only presence + the (safe) login + last status.
export interface GithubConnection {
  connected: boolean;
  login?: string | null;
  status?: "valid" | "invalid" | "unknown" | null;
  checkedAt?: string | null;
}
