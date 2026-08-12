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
  // BOOM_FEATURE_BOOKS: catalyst-books/audiobooks + the shared Amazon connect
  // flow. The AmazonConnectCard renders nothing when false.
  books_enabled: boolean;
}

// GET /api/v1/amazon — the shared Amazon device-connect status (catalyst-books
// + catalyst-audiobooks). Never carries the credential.
export interface AmazonConnection {
  connected: boolean;
  status?: string;
  checkedAt?: string;
}

// GET /api/v1/books/items — one siloed reading_items row (catalyst-books /
// catalyst-audiobooks). Mirrors internal/identity readingItemDTO; NEVER carries
// the raw source blob.
export interface ReadingItemDTO {
  source: string;
  externalId: string;
  title: string;
  authors: string;
  status: string;
  progressPercent: number;
  finished: boolean;
  startedAt?: string;
  finishedAt?: string;
  rating?: number;
  syncedAt: string;
  // Richer metadata (gaka-books) — optional; a low-fidelity source omits them.
  // Powers the Books page covers + fuller rows.
  coverUrl?: string;
  subtitle?: string;
  series?: string;
  narrators?: string;
  runtimeMin?: number;
  goodreadsRating?: number;
}

// GET /api/v1/hardcover — the Hardcover push-target connection status
// (catalyst-books). Mirrors internal/identity/hardcover_connect.go. NEVER
// carries the bearer token — only presence + last-known status + check time.
// status: 'valid' | 'invalid' | 'unknown'. The Jan-1 token reset makes
// 'invalid' a routine "please re-paste" signal.
export interface HardcoverConnection {
  connected: boolean;
  status?: string;
  checkedAt?: string;
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

// Admin jobs tab — GET /api/v1/admin/jobs + /schedules (gaka-hney). Mirrors
// internal/admin/jobs.go. Admin-gated (403 for non-admins); the tab is hidden
// from the sidebar for non-admins just like the other admin sections.
export type AdminJobStatus = "queued" | "running" | "done" | "failed";

export interface AdminJob {
  id: number;
  kind: string;
  status: AdminJobStatus;
  attempts: number;
  maxAttempts: number;
  error: string; // "" unless a run failed
  runAt: string; // RFC3339 — when the job becomes eligible to run
  createdAt: string;
  startedAt: string | null; // null until a worker picks it up
  finishedAt: string | null; // null until it terminates (done/failed)
}
export interface AdminJobsPayload {
  jobs: AdminJob[];
}
// One recurring, self-scheduling job kind. `nextRun` is the next fire time;
// `lastRun` is null until the kind has fired at least once this process.
export interface AdminJobSchedule {
  kind: string;
  intervalSeconds: number;
  nextRun: string; // RFC3339
  lastRun: string | null;
}
export interface AdminJobSchedulesPayload {
  schedules: AdminJobSchedule[];
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
