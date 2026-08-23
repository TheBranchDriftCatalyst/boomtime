# User Model & OIDC Readiness (boom-0oe scoping)

Status: **PLAN — not committed, awaiting review.** This document scopes the
substrate for (A) user tiers / RBAC and (B) OIDC-via-Authentik migration.
Deliverables from this scoping are this doc plus a set of sub-beads. No code
lands until each sub-bead is picked up individually.

---

## 1. Context — state of the world today

### 1.1 The `users` table

`internal/db/migrations/00001_initial_baseline.sql:324`:

```sql
CREATE TABLE public.users (
    username text NOT NULL,             -- PK
    hashed_password bytea NOT NULL,
    salt_used bytea NOT NULL,
    encrypted_wakatime_key bytea,       -- boom-6jm.2 (AES-256-GCM)
    wakatime_key_status text,           -- boom-6jm.10 (valid|invalid|null)
    wakatime_key_checked_at timestamptz,
    public_profile_enabled boolean DEFAULT false NOT NULL,  -- boom-6jm.1
    public_slug text,                                       -- boom-6jm.1
    argon_version integer DEFAULT 1 NOT NULL                -- boom-awh.6
);
```

Everything user-scoped in the schema (`heartbeats.sender`, `projects.owner`,
`refresh_tokens.owner`, `dashboard_layouts.owner`, `curation_rules.sender`,
`widget_defs.username`, `spaces.owner`, `badges.username`, `health_samples.owner`)
FKs back to `users.username`. There is no role, no tier, no group, no
capabilities column, no external identity column, no disabled/deleted flag.

### 1.2 Auth resolution

Three paths coexist today:

1. **API-token bearer** (`Authorization: Basic <base64>`) — used by editor
   plugins, the SPA after login, and every `/api/v1/users/current/*` handler.
   See `internal/handler/handler.go:162-177` (`resolveUser`) and
   `internal/db/auth.go:101-120` (`GetUserByToken`). Token is SHA-256 hashed
   at rest; `token_expiry IS NULL` means non-expiring API token.
2. **Refresh-cookie** (HttpOnly `refresh_token`) — used by `/auth/refresh_token`,
   `/auth/users/current`, and the import WebSocket handshake (which cannot
   carry an Authorization header). See `handler.go:130-150`
   (`resolveOwnerFromCookie`) and `db/auth.go:124-139`
   (`GetUserByRefreshToken`).
3. **Local password verify** (`internal/auth/service.go:65-74`,
   `VerifyUserCredentials`) — used only by `POST /auth/login` and by
   `POST /api/v1/users/current/password` (re-verify current before change).

`resolveUser` returns `(token, owner-username, apierr)`. Every gated handler
does:

```go
_, owner, aerr := h.resolveUser(c)
if aerr != nil { return respondErr(c, aerr) }
// ...owner-scoped work...
```

**Grep count:** 70 call sites of `resolveUser` / `resolveOwnerFromCookie`
across ~25 handler files (`grep -rn "resolveUser\|resolveOwnerFromCookie"
internal/handler/`). The auth resolver is the single choke point for
identity — the good news for both this substrate and any future OIDC swap.

There is currently **no notion** of:

- roles (admin vs regular vs read-only vs service account)
- tiers (light vs full user; who gets rollups)
- capabilities (per-endpoint gates beyond "authenticated?")
- account status (enabled / disabled without delete)
- external identity mapping (sub-claim, provider name, foreign user id)

### 1.3 Where rollups / expensive per-user machinery live

Grep for `hb_rollup_daily` / `health_rollup_daily`:

- `internal/db/ingest.go:70-77` — inside `SaveHeartbeats`, per affected
  sender, `recomputeGaps` + `refreshRollup` run in the SAME transaction as
  the raw insert. This is the primary write path for regular ingest AND for
  import jobs (via `w.db.SaveHeartbeats` in
  `internal/importer/importer.go:485`).
- `internal/db/ingest.go:190-234` (`RefreshRollup` / `refreshRollup`) —
  DELETE+INSERT rebuild bounded by `since`; also called by
  `RecomputeAllForSender` (line 335) after apply-rename or bulk changes.
- `internal/db/health.go:111-231` — the parallel path for health samples
  refreshes both `hb_rollup_daily` (workouts contribute to work-time via
  `workout_duration_s`) and `health_rollup_daily`.
- `internal/handler/derived.go` — `DerivedStatus` and `DerivedResync`
  endpoints let a user inspect / re-run their rollup.
- `internal/db/projects.go` — the `projects` table gets an upsert per unique
  `(sender, project)` pair on every heartbeat insert
  (`insertProjectsBatch` at `ingest.go:87-113`).

Storage cost is dominated by:

- `heartbeats` (raw) — one row per editor event, ~40 columns.
- `hb_rollup_daily` — pre-aggregated per (sender, day, project, language,
  editor, platform, machine, category, plugin, branch). One row per unique
  10-tuple per day.
- `health_rollup_daily` — one row per (owner, day, kind).
- `projects` — one row per (owner, project).

For a "light" user (public-only viewer, evaluator, service account) we want
to skip everything after the raw insert — no `refreshRollup`, no
`refreshHealthRollup`, no `insertProjectsBatch`. For a "read-only" user we
might want to reject ingest entirely at the handler layer.

### 1.4 Existing shape-of-the-repo patterns worth borrowing

Two migrations from this month show the shape the team already lives with:

- **`00032_dashboard_layouts.sql`** — new table with `owner TEXT REFERENCES
  users(username) ON DELETE CASCADE`, JSONB payload column, scope enum-as-string
  (unenforced at DB, validated at handler), `UNIQUE (owner, scope)`. The
  accessor comment (`internal/db/dashboard_layouts.go:6-12`) explicitly
  documents "we deliberately do NOT enum-validate scope so a future scope
  only needs handler wiring, not a migration + code change here." Future-
  proofing = string enum + handler-side allowlist.
- **`00033_curation_rules_enabled.sql`** — bare-simple `ALTER TABLE …
  ADD COLUMN enabled boolean NOT NULL DEFAULT true`. `internal/db/curation.go`
  layers ONE idempotent write helper (`SetCurationRuleEnabled` / `Toggle…`)
  plus a `WHERE enabled = true` filter in the load path. This is the pattern
  for a first-class boolean gate.

`curation_rules.action` is an unchecked TEXT enum-string (`CurationHide` /
`CurationRename` constants live in Go; DB doesn't enforce). The `spec` field
on `widget_defs`, `goals` (parallel bead boom-wpb), and `dashboard_layouts`
is JSONB with handler-side validation.

**Conclusion:** the codebase has settled on a hybrid pattern —
column-per-well-understood-flag (`enabled`, `public_profile_enabled`), plus
JSONB blob for tree-shaped data (`layout`, `spec`), plus TEXT enum-as-string
with handler-side allowlist (`scope`, `action`, `axis`). No enum types at the
DB level. No separate `roles`-style join table anywhere in the codebase.

### 1.5 Smallest OIDC-swap interface

To hit "OIDC swap doesn't touch every call site," the interface needs to be
narrower than the entire `resolveUser` signature. The 70 call sites all take
back `(token, owner-username, aerr)` today. If the interface returns
`(*Identity, aerr)` where `Identity` carries username plus per-request
capabilities plus the audit-shaped source-of-identity fields (provider name,
external sub, session id, expiry), we can:

- swap the resolver (local-token vs OIDC-bearer vs OIDC-cookie-session)
  without touching the handlers
- gate handlers by capability (`if !ident.Can(CapWriteBeats) { … }`) without
  making every handler know about tiers or providers.

Today's `_, owner, aerr := h.resolveUser(c)` becomes `ident, aerr :=
h.identify(c); owner := ident.Username`. The tuple-vs-struct migration is
the one mechanical refactor across all 70 sites; after that the identity
source is pluggable.

---

## 2. Recommendation

**Land RBAC-shaped capabilities as a small typed enum set persisted as a
JSONB blob on `users` (call it `capabilities`), with a normalized `role` TEXT
column for the coarse tier tag.** This is a hybrid choice, not a pure
enum-table or pure feature-flag store, and it matches the shape of the
existing codebase:

- **`role` TEXT** (like `curation_rules.action`, unenforced at DB, validated
  at Go layer): `full` (today's default; `NOT NULL DEFAULT 'full'`),
  `light` (no rollups / no projects table upserts), `service` (long-lived
  API token, no dashboard), `admin` (can act on other users). String enum
  keeps the schema-migration cost of adding a tier to one line.
- **`capabilities` JSONB** (like `dashboard_layouts.layout`,
  `widget_defs.spec`): sparse override map. Default derived from role; a
  populated JSON key overrides. Lets us tighten one specific capability for
  one user without minting a new role. Shape:
  `{"can_ingest_heartbeats": false, "can_generate_rollups": false,
  "storage_quota_bytes": 10485760}`. Handler-side allowlist of keys.
- **`disabled_at` TIMESTAMPTZ NULL**: soft-disable without delete. Every
  auth resolver path checks `disabled_at IS NULL` and fails-closed. Cheaper
  than a `WHERE users_status = 'active'` join and matches how the codebase
  handles time-based invalidations elsewhere.

**Why not pure feature-flags?** Feature flags scale to N users × M flags
poorly, and the audit story is worse ("who is a `service` user?" becomes
"which flag combination means service"). Roles are the correct primary key
for the human intent; capabilities are the fine-grained override.

**Why not pure roles (enum + separate `roles` join table)?** No join table
in the codebase today; single-column roles solve 90% of the need and stay
simple. The JSONB override lane covers the 10% (per-user quota, disable
one endpoint for one user) without paying join costs on every request.

**Why not an `enum` DB type?** The team's precedent (`curation_rules.action`,
`dashboard_layouts.scope`, `curation_rules.match_type`) is TEXT with
handler-side validation. Enums pay DDL cost on every value addition; TEXT
does not.

**How this meets the OIDC ask:** Authentik returns a `groups` claim.
Groups-to-role is a config-driven map (`BOOM_AUTHENTIK_GROUP_TO_ROLE=
boomtime-full:full,boomtime-light:light,boomtime-admin:admin`) applied at
the identity-provisioning layer. Capabilities-per-group is a follow-up (map
one Authentik group to a JSONB capability override). See §5.

---

## 3. Schema sketch

New migration **`00034_user_model_substrate.sql`** (numbering sequential
after 00033_curation_rules_enabled and the parallel goals bead boom-wpb,
which is planned as 00034 — coordinate ordering on merge; this bead may need
to become 00035 or 00036 depending on boom-wpb landing order):

```sql
-- +goose Up
-- +goose StatementBegin

-- User-model substrate (boom-0oe). Adds:
--   role         TEXT    NOT NULL DEFAULT 'full'
--   capabilities JSONB   NOT NULL DEFAULT '{}'::jsonb
--   disabled_at  TIMESTAMPTZ NULL
--
-- All three columns are additive and default-safe: EXISTING rows land at
-- role='full', capabilities='{}', disabled_at=NULL — which is the identity
-- element for the new gating logic (see /internal/auth/capability.go).
-- Every existing test and every existing prod behavior therefore stays
-- byte-for-byte identical UNTIL the BOOM_FEATURE_USER_MODEL flag flips on.
--
-- role values validated in Go (auth.ValidRoles): 'full' | 'light' |
-- 'service' | 'admin'. TEXT (not a Postgres enum) so adding a tier stays
-- a one-line Go change (matches curation_rules.action pattern).
--
-- capabilities keys validated in Go (auth.KnownCapabilityKeys). Default is
-- '{}' — the resolver derives effective capabilities from (role, overrides)
-- so an empty JSON blob means "use the role's defaults."
--
-- disabled_at NULL = active. When set, every auth resolver path
-- (GetUserByToken, GetUserByRefreshToken, VerifyUserCredentials) MUST
-- fail-closed. See internal/db/auth.go handler wrapping in the bead
-- "boom-0oe.1 substrate: schema + disabled_at fail-closed."
ALTER TABLE public.users
    ADD COLUMN role         TEXT        NOT NULL DEFAULT 'full',
    ADD COLUMN capabilities JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN disabled_at  TIMESTAMPTZ NULL;

-- Fast-path for "list disabled" / audit; also lets the resolver skip a
-- disabled_at IS NULL scan when the column is fully populated.
CREATE INDEX users_disabled_at_idx ON public.users (disabled_at)
    WHERE disabled_at IS NOT NULL;

-- External identity linkage (Authentik / future OIDC providers).
-- One row per user, per provider. NULL rows for local-password users. A
-- user can have BOTH a local password AND an OIDC link during migration.
-- Deleted with the user (ON DELETE CASCADE).
--
-- sub is the OIDC-canonical stable subject identifier from the provider.
-- provider is 'authentik' today; future providers add rows with different
-- provider names. UNIQUE (provider, sub) prevents two boomtime accounts
-- claiming the same external identity.
--
-- The `claims` JSONB caches the last-seen non-sensitive claim payload
-- (email, preferred_username, groups) for audit + admin UI. Never contains
-- id_token / access_token / refresh_token — those stay in refresh_tokens /
-- auth_tokens (which already have the hashed-at-rest treatment from
-- boom-b5x.2). See "identity resolver interface" §5.
CREATE TABLE public.user_external_identities (
    id           UUID        PRIMARY KEY DEFAULT public.uuid_generate_v4(),
    username     TEXT        NOT NULL REFERENCES public.users(username) ON DELETE CASCADE,
    provider     TEXT        NOT NULL,
    sub          TEXT        NOT NULL,
    email        TEXT,
    claims       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, sub)
);

CREATE INDEX user_external_identities_username_idx ON public.user_external_identities(username);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_external_identities;
DROP INDEX IF EXISTS public.users_disabled_at_idx;
ALTER TABLE public.users
    DROP COLUMN IF EXISTS role,
    DROP COLUMN IF EXISTS capabilities,
    DROP COLUMN IF EXISTS disabled_at;
-- +goose StatementEnd
```

**Migration is additive and safe when the feature flag is off.** No code
path reads `role`, `capabilities`, `disabled_at`, or
`user_external_identities` when `BOOM_FEATURE_USER_MODEL=off` (the default).
Existing tests pass unchanged.

---

## 4. Capability check middleware / helper

Two new files:

- `internal/auth/capability.go` — pure types + role-to-capability defaults.
- `internal/auth/identity.go` — the `Identity` struct + the tuple-shaped
  compat helper.

### 4.1 `internal/auth/capability.go` (sketch)

```go
package auth

// Capability names — enumerate, don't stringly-type at call sites.
type Capability string

const (
    CapIngestHeartbeats  Capability = "ingest_heartbeats"
    CapGenerateRollups   Capability = "generate_rollups"     // gates refreshRollup/refreshHealthRollup
    CapReadDashboards    Capability = "read_dashboards"       // stats/timeline/leaderboards/projects
    CapEditCuration      Capability = "edit_curation"         // curation_rules CRUD
    CapEditSpaces        Capability = "edit_spaces"
    CapEditWidgets       Capability = "edit_widgets"
    CapImportWakatime    Capability = "import_wakatime"       // /import*
    CapBackupExport      Capability = "backup_export"
    CapBackupImport      Capability = "backup_import"
    CapPublicProfile     Capability = "public_profile"        // opt-in gate
    CapChangePassword    Capability = "change_password"       // OIDC users lose this
    CapAdmin             Capability = "admin"                 // impersonate, list-users, disable-user
)

// Role — coarse tier. Persists to users.role (TEXT).
type Role string

const (
    RoleFull    Role = "full"    // today's default; everything on
    RoleLight   Role = "light"   // read + curation; NO rollup gen, NO projects upsert, NO ingest
    RoleService Role = "service" // long-lived API token; ingest yes, dashboards no
    RoleAdmin   Role = "admin"   // full + CapAdmin
)

// ValidRoles is the handler-side allowlist mirror of the DB column. New
// tiers land here first, then in the docs. Adding a role does NOT require
// a migration (the column is TEXT).
var ValidRoles = map[Role]struct{}{
    RoleFull: {}, RoleLight: {}, RoleService: {}, RoleAdmin: {},
}

// roleDefaults returns the baseline capability set for a role. Layered
// under any explicit users.capabilities JSONB override.
func roleDefaults(r Role) map[Capability]bool {
    switch r {
    case RoleLight:
        return map[Capability]bool{
            CapReadDashboards: true, CapEditCuration: true, CapPublicProfile: true,
            CapChangePassword: true,
            // deliberately absent: CapIngestHeartbeats, CapGenerateRollups,
            // CapImportWakatime, CapBackupExport, CapBackupImport
        }
    case RoleService:
        return map[Capability]bool{
            CapIngestHeartbeats: true, CapGenerateRollups: true,
        }
    case RoleAdmin:
        m := roleDefaults(RoleFull)
        m[CapAdmin] = true
        return m
    default: // RoleFull
        return map[Capability]bool{
            CapIngestHeartbeats: true, CapGenerateRollups: true,
            CapReadDashboards: true, CapEditCuration: true, CapEditSpaces: true,
            CapEditWidgets: true, CapImportWakatime: true,
            CapBackupExport: true, CapBackupImport: true,
            CapPublicProfile: true, CapChangePassword: true,
        }
    }
}
```

### 4.2 `internal/auth/identity.go` (sketch)

```go
package auth

// Identity is the resolved bag every gated handler works from. Replaces
// today's `(token, owner-username)` tuple. Carries per-request capability
// state so handlers don't re-query the DB per gate check.
type Identity struct {
    Username   string
    Role       Role
    Provider   string // "local" | "authentik" | ...
    caps       map[Capability]bool
    Token      string // for handlers that still need to pass the raw token (change-password uses this to skip revoking the caller's own session)
    // Session bookkeeping
    ExpiresAt  time.Time
}

// Can reports whether the identity has a capability. Layered: role defaults
// underneath, then the users.capabilities JSONB override on top.
func (i *Identity) Can(c Capability) bool {
    if i == nil { return false }
    v, ok := i.caps[c]
    return ok && v
}

// buildIdentity merges role defaults with the JSONB override.
func buildIdentity(u *StoredUserFull, provider, token string) *Identity {
    caps := roleDefaults(u.Role)
    for k, v := range u.CapabilitiesOverride {
        caps[Capability(k)] = v
    }
    return &Identity{
        Username: u.Username,
        Role:     u.Role,
        Provider: provider,
        caps:     caps,
        Token:    token,
    }
}
```

### 4.3 Handler-side call-site shape

Contrast today vs target. Today (`handler/curation.go:24`):

```go
_, owner, aerr := h.resolveUser(c)
if aerr != nil { return respondErr(c, aerr) }
```

Target — behind the feature flag, EVERY handler wraps `resolveUser` with a
capability check on the operations it performs:

```go
ident, aerr := h.identify(c)
if aerr != nil { return respondErr(c, aerr) }
if !ident.Can(auth.CapEditCuration) { return respondErr(c, apierr.Forbidden()) }
owner := ident.Username
```

**When the flag is off**, `h.identify(c)` returns an `Identity` whose `caps`
map is populated to allow EVERY capability (mimicking today's "authenticated
= can-do-anything" model). Handlers ALWAYS pass through their `.Can` checks.
No behavior change.

**When the flag is on**, `h.identify(c)` looks up
`(users.role, users.capabilities, users.disabled_at)` and returns the real
`Identity`. Handlers gate correctly. A `light` user hitting
`POST /api/v1/users/current/curation` gets 403; a `light` user hitting
`GET /api/v1/users/current/stats` still works.

The compat shim `h.resolveUser` keeps its today-signature and is kept
during the migration so unmigrated call sites still compile. Sub-beads
migrate them file-by-file.

---

## 5. Index-skip / rollup-skip flag propagation

The rollup / projects-upsert work is inside `db.SaveHeartbeats`
(`internal/db/ingest.go:28-83`), specifically the last block:

```go
for sender, since := range minBySender {
    if err := recomputeGaps(ctx, tx, sender, since); err != nil { return nil, err }
    if err := refreshRollup(ctx, tx, sender, since); err != nil { return nil, err }
}
```

Two clean intervention points:

- **`insertProjectsBatch` (line 45)** — skip when caller says so; light
  users don't need the `projects` table populated.
- **`refreshRollup` / `refreshHealthRollup`** — skip when caller says so.
  Raw heartbeats still land (needed for public-profile reads and for
  potential future backfill), but derived storage stays empty.

Two options for propagation, ordered by preference:

1. **[chosen] Pre-check at the handler**: the ingest handlers
   (`internal/handler/heartbeats.go`, `internal/handler/import.go`) resolve
   `Identity` first. If `!ident.Can(auth.CapIngestHeartbeats)` → 403 before
   any DB work. If `!ident.Can(auth.CapGenerateRollups)` → they call a NEW
   `db.SaveHeartbeatsRaw(ctx, hbs, opts)` variant that skips phase-3
   (rollup + gap recompute + projects upsert). Existing `SaveHeartbeats`
   stays the "everything" default so no unmigrated caller regresses.

2. Threading a `SaveHeartbeatsOptions{SkipRollup bool, SkipProjects bool}`
   through the existing signature. Cheaper to grep for but changes every
   caller (there are only ~3 today: heartbeats handler, workouts handler,
   importer worker), which is fine — the sub-bead for this can add the
   options struct and rename the method to `SaveHeartbeatsWithOptions`, or
   just add a second variant.

**Sub-bead plan**: `db.SaveHeartbeats` grows a sibling
`db.SaveHeartbeatsRaw` that runs phases 1+2 (project upsert + heartbeat
insert) but NOT phase 3 (gap + rollup recompute). The handler chooses
which based on `ident.Can(auth.CapGenerateRollups)`. Flag-off preserves the
current path (every ingest calls the full `SaveHeartbeats`); flag-on lets
`light` users skip.

Also: importer worker (`internal/importer/importer.go:485`) uses the same
`w.db.SaveHeartbeats`, so it inherits the same behavior — a `light` user
who somehow submits an import gets 403 at the handler (before the worker
starts). The worker itself doesn't need to know about capabilities.

Follow-up under the same substrate bead: **quota enforcement**. A `light`
user has a storage cap in `users.capabilities.storage_quota_bytes`. The
ingest handler consults `db.UserStorageBytes(owner)` (already exists as
part of `DerivedStatus` — `internal/db/ingest.go:270-283`
`pg_total_relation_size`) and rejects if over quota. Not first-slice work;
files as a follow-up sub-bead.

---

## 6. OIDC-shaped identity interface (Authentik-specific)

### 6.1 Target provider: Authentik

Authentik is a standards-compliant OIDC provider. The user already runs it
in their Talos k8s homelab for other apps and will spin up a dev instance
alongside the existing `docker-compose.yml` postgres. Because Authentik
follows the OIDC spec cleanly, the interface below is standard-OIDC-shaped;
provider-specific quirks are called out inline.

Reference: https://docs.goauthentik.io/docs/providers/oauth2 and RFC 8414
(OAuth 2.0 Authorization Server Metadata) / OpenID Connect Discovery 1.0.

### 6.2 Endpoints (all standard OIDC / discoverable via issuer URL)

Assuming `BOOM_OIDC_ISSUER=https://authentik.<host>/application/o/boomtime/`,
boomtime resolves the endpoints below by fetching
`${ISSUER}/.well-known/openid-configuration` at startup and caching for the
process lifetime:

- **`authorization_endpoint`** — `${ISSUER}authorize/` — browser redirect for
  the auth code flow.
- **`token_endpoint`** — `${ISSUER}token/` — code exchange + refresh.
- **`userinfo_endpoint`** — `${ISSUER}userinfo/` — post-token claim fetch
  (used only if we don't decode the id_token directly).
- **`jwks_uri`** — `${ISSUER}jwks/` — RSA/ECDSA keys for id_token signature
  verification.
- **`end_session_endpoint`** — `${ISSUER}end-session/` — Authentik
  supports RP-initiated logout.

Authentik-specific detail: the issuer path always ends in
`/application/o/<application-slug>/` (with the trailing slash). The
application slug is set in Authentik admin when the OIDC application is
created; the boomtime application must be created there before dev/prod can
run. This is a manual setup step for the operator, documented in the dev
subsection §6.5.

### 6.3 Claims Authentik sends

Standard OIDC id_token claims from Authentik:

- `sub` — stable, canonical user identifier. This is what
  `user_external_identities.sub` stores.
- `email` — user's email (Authentik requires the `email` scope).
- `preferred_username` — the Authentik login username. Boomtime uses this
  as the default `users.username` on first-login provisioning (with a
  fallback to the localpart of `email` if absent).
- `groups` — array of Authentik group names the user belongs to. Requires
  the `groups` scope be enabled in the Authentik OIDC application (default
  ON in current Authentik).

Authentik convention: `groups` is a flat string array
(`["boomtime-full", "authentik Admins"]`). The mapping to boomtime role is:

```
BOOM_AUTHENTIK_GROUP_TO_ROLE=boomtime-admin:admin,boomtime-full:full,boomtime-light:light
```

First match wins. If none matches, default to `RoleLight` (fail-closed for
tier defaults — a random Authentik user who happens to hit the OIDC flow
lands on the cheapest tier).

### 6.4 The `IdentityResolver` interface

```go
package auth

// IdentityResolver is the pluggable identity boundary. LocalPasswordResolver
// (today's behavior) implements it; OIDCResolver (Authentik) is a second
// implementation. Boomtime's config picks one via BOOM_AUTH_PROVIDER (local|oidc).
type IdentityResolver interface {
    // ResolveBearer takes a raw Authorization header value ("Basic <base64>"
    // for local API tokens, "Bearer <jwt>" for OIDC access tokens) and
    // returns an Identity or an *apierr.Error.
    ResolveBearer(ctx context.Context, headerValue string) (*Identity, *apierr.Error)

    // ResolveCookie takes the refresh_token cookie and returns Identity.
    // For OIDC, the "refresh_token" cookie's value is boomtime's own
    // opaque session id (mapping to a server-side OIDC session record);
    // the actual OIDC refresh_token stays server-side.
    ResolveCookie(ctx context.Context, cookieValue string) (*Identity, *apierr.Error)

    // BeginLogin is called by POST /auth/login. For LocalPasswordResolver
    // this verifies password + mints tokens (today's behavior). For
    // OIDCResolver it returns a 302 redirect to the authorization endpoint
    // (via the returned LoginRedirect struct); the caller wraps it into an
    // Echo redirect response.
    BeginLogin(ctx context.Context, req LoginRequest) (LoginResult, *apierr.Error)

    // CompleteLogin handles the OAuth callback (OIDCResolver only; the
    // local resolver returns 404 for the callback route). Verifies id_token
    // signature via JWKS, provisions the user if new, mints boomtime session.
    CompleteLogin(ctx context.Context, code, state string) (*Identity, LoginResult, *apierr.Error)

    // Logout revokes the boomtime session and, for OIDC, optionally hits
    // Authentik's end_session_endpoint.
    Logout(ctx context.Context, ident *Identity) *apierr.Error
}
```

Two implementations:

- **`LocalPasswordResolver`** — thin wrapper over today's
  `GetUserByToken` / `GetUserByRefreshToken` / `VerifyUserCredentials`.
  Behavior identical to today.
- **`OIDCResolver`** — deferred to a later bead
  (`boom-0oe.oidc-resolver`, marked `--defer`). Uses `github.com/coreos/
  go-oidc/v3/oidc` for discovery/verification and the stdlib
  `golang.org/x/oauth2` for the code-exchange flow. Not landing in this
  scoping; the interface goes in now so the compat shim `h.identify(c)`
  can wire against it and the swap is a one-line constructor change later.

### 6.5 Provisioning flow (first Authentik login)

1. Browser hits `/auth/login/oidc` → 302 → Authentik `authorize/` with
   `state`, `nonce`, `redirect_uri=https://boomtime.host/auth/callback/oidc`.
2. User logs in on Authentik.
3. Authentik 302s back to `/auth/callback/oidc?code=…&state=…`.
4. Boomtime calls `CompleteLogin`:
   - Exchange `code` at token endpoint → get id_token.
   - Verify id_token signature via JWKS.
   - Extract `sub`, `email`, `preferred_username`, `groups`.
   - Look up `user_external_identities` on `(provider='authentik', sub=…)`.
     - **Found** → resolve to that `users.username`, refresh `claims` +
       `last_seen_at`. Recompute role from `groups` (in case Authentik
       groups changed; this MAY downgrade a tier — that's the point of
       centralizing tiers on the provider).
     - **Not found**, `email` matches an existing `users.username` OR
       `users.email` (if we add that column later) → **link mode**: this is
       an admin-configurable behavior (`BOOM_OIDC_AUTOLINK_EMAIL=false`
       default). Fail-safe default is DON'T auto-link — a matching-email
       account might be a coincidence. When enabled, admin has explicitly
       opted in; write `user_external_identities` row and continue.
     - **Not found**, no email match, `BOOM_OIDC_AUTOPROVISION=false`
       default → 403 "no boomtime account for this identity; ask admin to
       create one." When `BOOM_OIDC_AUTOPROVISION=true`, mint a new
       `users` row with username = `preferred_username` (uniquified with a
       numeric suffix if taken), role from `groups`, no password
       (`hashed_password = ''`; the local login path is disabled for this
       user via `capabilities.can_change_password=false`).
5. Mint a boomtime session cookie (opaque id → server-side session record
   holding the id_token expiry + refresh_token). Redirect to `/`.

**Sub-claim change / rotation**: Authentik `sub` is stable per
(application, user). If Authentik admin re-creates the application, `sub`
changes for every user. This is an operational hazard, not a code fix —
document it in the ops runbook and rely on the `provider + sub` UNIQUE
constraint failing loudly (login path returns 500 "external identity
conflict"). The admin-triggered relink CLI (`boomtime user relink-oidc
--user <username> --new-sub <sub>`) is a follow-up sub-bead.

### 6.6 Dev story — Authentik in docker-compose

Add three services to `docker-compose.yml` (behind a profile so `docker
compose up` without the profile stays today's minimal stack). Sketch:

```yaml
services:
  # ... existing db, app, web ...

  authentik-postgres:
    profiles: ["oidc"]
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: authentik
      POSTGRES_USER: authentik
      POSTGRES_PASSWORD: authentik
    volumes:
      - authentik_db_data:/var/lib/postgresql/data

  authentik-redis:
    profiles: ["oidc"]
    image: redis:7-alpine

  authentik:
    profiles: ["oidc"]
    image: ghcr.io/goauthentik/server:2025.10
    command: server
    environment:
      AUTHENTIK_SECRET_KEY: dev-only-not-a-secret
      AUTHENTIK_POSTGRESQL__HOST: authentik-postgres
      AUTHENTIK_POSTGRESQL__USER: authentik
      AUTHENTIK_POSTGRESQL__NAME: authentik
      AUTHENTIK_POSTGRESQL__PASSWORD: authentik
      AUTHENTIK_REDIS__HOST: authentik-redis
    ports:
      - "9000:9000"
      - "9443:9443"
    depends_on:
      - authentik-postgres
      - authentik-redis

  authentik-worker:
    profiles: ["oidc"]
    image: ghcr.io/goauthentik/server:2025.10
    command: worker
    environment:
      # ... same as authentik ...
    depends_on: [authentik]

volumes:
  authentik_db_data: {}
```

Run with `docker compose --profile oidc up`. Wait for Authentik to bootstrap
(first-run wizard on http://localhost:9000), create a boomtime application
with slug `boomtime`, note the client_id / client_secret, then set in
`.env`:

```
BOOM_AUTH_PROVIDER=local  # keep local until you're ready to switch
BOOM_OIDC_ISSUER=http://authentik:9000/application/o/boomtime/
BOOM_OIDC_CLIENT_ID=<from authentik>
BOOM_OIDC_CLIENT_SECRET=<from authentik>
BOOM_OIDC_REDIRECT_URL=http://localhost:8080/auth/callback/oidc
BOOM_AUTHENTIK_GROUP_TO_ROLE=boomtime-admin:admin,boomtime-full:full,boomtime-light:light
BOOM_OIDC_AUTOPROVISION=true   # dev convenience; false in prod
BOOM_OIDC_AUTOLINK_EMAIL=false # ALWAYS false in prod
```

Boomtime auto-discovers the endpoints via
`${BOOM_OIDC_ISSUER}.well-known/openid-configuration` at startup — no need
to hand-configure the sub-endpoints.

### 6.7 Prod story — Talos k8s

The user already runs Authentik in their Talos cluster for homelab apps.
Adding boomtime is a matter of:

1. Create an OIDC application in the existing Authentik with slug
   `boomtime`, redirect URL `https://boomtime.<host>/auth/callback/oidc`.
2. Create groups `boomtime-full`, `boomtime-light`, `boomtime-admin`;
   assign users.
3. Add `BOOM_OIDC_*` env vars to `k8s/base/configmap.yaml` (issuer,
   client_id, redirect URL, group-to-role mapping,
   `BOOM_OIDC_AUTOPROVISION=false`, `BOOM_OIDC_AUTOLINK_EMAIL=false`).
4. Add `BOOM_OIDC_CLIENT_SECRET` to the ExternalSecret in
   `k8s/overlays/talos00-knowledgedump/external-secret.yaml` (source it
   from wherever the user keeps other Authentik client secrets).
5. Flip `BOOM_AUTH_PROVIDER=oidc` in the overlay AFTER dev validation and
   AFTER the feature-flag rollout plan (§7) says so.

**Open question — needs user input at review time**: what's the exact
Authentik issuer URL in the user's Talos cluster (external DNS name, path
prefix)? And what naming convention do the existing Authentik apps use for
their ExternalSecret refs? This document assumes both but the coordinator
noted I can't reach the cluster from this environment — the user should
paste the relevant `configmap.yaml` / `external-secret.yaml` chunk from an
existing app during review so we mirror the convention exactly.

### 6.8 Which call sites refactor

The `IdentityResolver` interface adds a new indirection, but call-site
churn is bounded:

- **The 70 handler call sites** to `resolveUser` / `resolveOwnerFromCookie`
  migrate to `h.identify(c)` returning `*Identity`. Mechanical
  find-and-replace; each file can be its own commit. **This is the
  churn-heavy refactor.** Sub-bead breakdown estimates 4-6 focused
  sub-beads (auth.go, curation.go, spaces.go, widgets.go, stats-batch,
  ingest-batch), each ~10-15 sites.
- **`handler/auth.go`** — `Login`, `RefreshToken`, `Logout` call
  `IdentityResolver.BeginLogin` / `CompleteLogin` / `Logout`. This is where
  the OIDC callback route gets added (guarded by resolver = `oidc`).
- **`internal/db/auth.go`** — the raw lookups (`GetUserByToken`,
  `GetUserByRefreshToken`) stay put; `LocalPasswordResolver` wraps them.
  `OIDCResolver` uses a new set of accessors on `user_external_identities`
  + a server-side session table (`oidc_sessions` — schema TBD in the
  OIDC-resolver bead).
- **`internal/importer/importer.go`** — no change. The worker takes an
  already-authenticated `owner`; it doesn't care how identity was resolved.
- **`internal/auth/service.go`** — `CreateUser` grows a `role` parameter
  (default `RoleFull` for existing callers, so the CLI keeps working
  unchanged). `CreateAPIToken` unchanged.

---

## 7. Feature-flag rollout plan

### 7.1 Env flags

Three flags govern the substrate; all default to values that produce
today's behavior:

- **`BOOM_FEATURE_USER_MODEL=off`** (default `off`). When `off`,
  `h.identify(c)` returns an all-caps-true `Identity` and no handler ever
  observes a capability denial. When `on`, the resolver reads the real
  `role` + `capabilities` and gates as designed. Migrations still run and
  columns still exist regardless — this flag only affects READ / GATE paths.
- **`BOOM_AUTH_PROVIDER=local`** (default `local`). Picks the
  `IdentityResolver` implementation. `local` = today's behavior. `oidc` =
  Authentik. Registered at server-New time; can't change without a restart.
- **`BOOM_FEATURE_ROLLUP_SKIP=off`** (default `off`). When `off`,
  `SaveHeartbeats` always runs phase 3 (rollup + gap + projects). When
  `on`, the ingest handlers consult `ident.Can(CapGenerateRollups)` and
  dispatch to `SaveHeartbeatsRaw` when denied. Requires
  `BOOM_FEATURE_USER_MODEL=on` to have any effect (a check-fails-open in
  the handler is a no-op if every ident has every cap).

### 7.2 What each flag gates at each substrate layer

| Layer | `BOOM_FEATURE_USER_MODEL=off` | `BOOM_FEATURE_USER_MODEL=on` |
|---|---|---|
| Migration | Runs (adds columns unused) | Runs |
| `db.GetUserByToken` | Returns owner; ignores `disabled_at` | Returns owner; fail-closed when `disabled_at IS NOT NULL` |
| `h.identify(c)` | Returns all-caps-true Identity | Returns real Identity from `role` + `capabilities` |
| `ident.Can(cap)` in handlers | Always true | Real gate |
| `SaveHeartbeats` phase 3 | Always runs | Skipped when `BOOM_FEATURE_ROLLUP_SKIP=on` AND `!ident.Can(CapGenerateRollups)` |
| `/healthz` | Reports `feature_user_model: off` | Reports `feature_user_model: on`, plus `auth_provider: local\|oidc` |

| Layer | `BOOM_AUTH_PROVIDER=local` | `BOOM_AUTH_PROVIDER=oidc` |
|---|---|---|
| Login form | Today's `/auth/login` | 302 to Authentik `authorize/` |
| `/auth/callback/oidc` route | Registered but returns 404 | Live callback handler |
| `/auth/register` route | Live (subject to `BOOM_ENABLE_REGISTRATION`) | 404 (registration flows through Authentik) |
| Bearer token check | `hashSessionToken` + `auth_tokens` lookup | id_token JWT verify via JWKS |
| Cookie session | `refresh_tokens` table lookup | `oidc_sessions` table lookup |

### 7.3 Prod deploy sequence

1. **Land the schema-only bead** (`boom-0oe.substrate-schema`): migration
   00034 ships. Columns are NOT NULL DEFAULT populated for existing rows.
   Feature flag off. Zero code path reads new columns. Full test suite
   passes. Deploy. Prod is byte-for-byte identical.
2. **Land the identity-shim bead** (`boom-0oe.identity-shim`): `Identity`
   struct + `h.identify(c)` compat shim + 70-site refactor (bundled across
   4-6 sub-beads, each ~10-15 sites). Feature flag STILL OFF; every
   `identify` returns all-caps-true. Full test suite passes AND a new
   feature-flag-off regression test runs the existing integration suite
   against the shim path. Deploy. Prod still byte-for-byte identical.
3. **Land the capability-gate bead** (`boom-0oe.capability-gates`): each
   handler adds its `ident.Can(…)` check. Feature flag STILL OFF; every
   check still passes. Deploy. Prod still identical.
4. **Land the rollup-skip bead** (`boom-0oe.rollup-skip`): ingest
   dispatches to `SaveHeartbeatsRaw` when capability denies. Feature flag
   still off. Deploy. Prod still identical.
5. **Land the admin CLI bead** (`boom-0oe.admin-cli`): `boomtime user
   set-role --user X --role light` operator command. No API exposure yet.
6. **Flip flag in dev**: `BOOM_FEATURE_USER_MODEL=on` +
   `BOOM_FEATURE_ROLLUP_SKIP=on` in local dev. Manually test tier
   downgrade → verify rollups stop, dashboards degrade gracefully.
7. **Canary flip in prod**: one user is single-tenant, so "canary" = the
   user flips the flag on their own instance behind a maintenance window.
   Watches `/healthz` + logs.
8. **Default flip** (much later, when the user is confident):
   `BOOM_FEATURE_USER_MODEL=on` becomes the shipped default in
   `configmap.yaml` and `.env.example`.

OIDC provider swap is a PARALLEL track: land the schema (already covered
above), land the `IdentityResolver` interface + `LocalPasswordResolver`
implementation, land the `OIDCResolver` implementation (deferred bead), then
flip `BOOM_AUTH_PROVIDER=oidc` in the same canary-then-default pattern.

### 7.4 Observability

The `/healthz` payload gains a `features` object:

```json
{
  "status": "ok",
  "features": {
    "user_model": "off",
    "auth_provider": "local",
    "rollup_skip": "off"
  }
}
```

This gives ops a one-shot check of which substrate is active in a running
deploy. Logs at boot ALSO structured-log the effective feature-flag map
(matches the existing pattern for `BOOM_CORS_ALLOWED_ORIGINS` /
`BOOM_ENCRYPTION_KEY` boot logs).

### 7.5 Killswitch

Every flag is env-only; there is no DB kill switch. Reverting a bad flip
is: edit `configmap.yaml`, `kubectl apply`, pod restart. No image rebuild.
Total revert time ≈ pod restart time (< 30s).

If a substrate BUG somehow manifests when the flag is off (which the
tests-both-paths regression should prevent), the fix is to revert the code
image. This is why each sub-bead's acceptance criteria include the
flag-off-no-change regression.

---

## 8. First-slice execution boundary

The single first-slice bead to pick up is
**`boom-0oe.1 substrate: schema + identity struct + flag-off compat shim`**
(exact bead ID assigned when filed via `bd create`).

**Scope**:
- Migration 00034 (schema only; see §3).
- `internal/auth/capability.go` — the `Capability` / `Role` enum types +
  `roleDefaults()` map.
- `internal/auth/identity.go` — the `Identity` struct + `buildIdentity()`
  helper + `Can()` method.
- `internal/db/rows.go` — extend `StoredUser` to a NEW `StoredUserFull`
  struct that includes `Role`, `Capabilities`, `DisabledAt` (leave the
  today `StoredUser` alone so unmigrated callers still compile).
- `internal/db/auth.go` — a new `GetUserFullByName` reader (leave the
  today `GetUserByName` alone).
- `internal/config/config.go` — add `FeatureUserModel bool` (from
  `BOOM_FEATURE_USER_MODEL`, default false) and plumb through.
- `internal/handler/handler.go` — new `h.identify(c)` that returns an
  all-caps-true `Identity` when `!cfg.FeatureUserModel`, and reads the real
  columns when true. `h.resolveUser` stays untouched (compat shim).
- `internal/handler/healthz.go` — surface the `features` object in
  `/healthz` (§7.4).
- Tests:
  - Existing full test suite passes UNCHANGED with flag off.
  - A NEW test with `BOOM_FEATURE_USER_MODEL=on` runs a subset of handlers
    with a `RoleLight` user and asserts capability denials on ingest,
    import, backup; asserts allow on read/curation/dashboards.
  - A `disabled_at IS NOT NULL` user is denied by all three auth paths
    (bearer, cookie, password verify) when flag is on.

**Why this is highest-value / least-risk**:
- **De-risks the shape** of `Identity` / `Can` / `role` before any handler
  refactor. If we got the shape wrong, we notice here, in a scope of ~5
  files.
- **Zero user-visible change** (flag defaults off, schema is additive).
  Regression risk is minimal — the existing suite gates the flag-off path.
- **Unblocks parallel work**. Once `Identity` + `IdentityResolver` shape
  is in, the 70-site refactor is mechanical and can be split across as
  many sub-beads as convenient.
- **One session's work**. Migration + 4 new/edited files + tests fits.

Explicitly OUT of scope for the first slice:
- `IdentityResolver` interface (comes with the resolver-shim bead so it
  lands with `LocalPasswordResolver` in one coherent chunk).
- 70-site handler refactor (many sub-beads, done after the shape is
  proven).
- `SaveHeartbeatsRaw` variant (rollup-skip bead).
- Admin CLI for role assignment (own bead).
- OIDC resolver (deferred bead).

---

## 9. Risks & unknowns

### 9.1 Real risks

- **Migration ordering with boom-wpb (Goals)**: the goals bead is planning
  migration 00034. This bead ALSO tentatively references 00034. Whichever
  lands first keeps the number; the second renumbers. Coordinate in the
  bead ordering — probably just note in the sub-bead acceptance that the
  migration number is "next available at merge time."
- **Test seam for OIDC**: `github.com/coreos/go-oidc/v3/oidc` requires a
  reachable issuer to construct a verifier. Tests need a mock issuer
  (spin up a small `httptest.Server` that returns a canned discovery doc +
  JWKS). This is out-of-scope for the first slice, but the OIDC-resolver
  bead should budget for the test harness.
- **Session-cookie shape change on OIDC swap**: today `refresh_token`
  cookie carries a UUID that looks up `refresh_tokens`. OIDC needs a
  server-side session record (holds the id_token expiry + provider
  refresh_token). Options: (a) reuse `refresh_tokens` table with a
  provider column, (b) new `oidc_sessions` table. Recommendation: (b) —
  the shapes are different enough (OIDC session needs id_token_hint for
  RP-initiated logout) that co-mingling risks confusion. Defer decision
  to the OIDC-resolver bead.
- **Login-flow smoke tests** for BOTH providers in CI: today's tests only
  cover local login. When `OIDCResolver` lands we need a mock-Authentik
  fixture so CI verifies the OAuth code-exchange path. Non-trivial;
  budget in the OIDC-resolver bead.
- **User rename story with OIDC provisioning**: if a user is auto-provisioned
  from Authentik with `preferred_username=alice` and then the Authentik admin
  renames them to `alice-new`, boomtime doesn't observe the rename (the `sub`
  is stable). The relationship stays correct but the `users.username` is
  stale. Fix: display `preferred_username` from `claims` for the UI, keep
  `users.username` as the internal id. Follow-up bead, not first-slice.

### 9.2 Genuine unknowns / needs user input

- **Authentik issuer URL in the user's Talos cluster** — the exact external
  DNS name and application-slug convention. This doc uses
  `https://authentik.<host>/application/o/boomtime/` as a placeholder.
  Coordinator flagged this — please paste the relevant chunk from an
  existing app's configmap during review.
- **Business model implication**: does the "light tier" imply future SaaS
  positioning, or is it purely for personal-instance evaluators / a
  service account for a self-hosted CI writing heartbeats? If SaaS,
  quota-enforcement gets billing implications and needs a full-fledged
  billing subsystem beyond this scoping. Assume personal-instance for the
  purposes of this doc; if SaaS, this substrate is still the right first
  layer but many follow-ups get bigger.
- **Deployment target for OIDC in prod**: this doc assumes single-instance
  Talos-hosted boomtime + single-tenant Authentik. If a future multi-
  instance or multi-tenant deploy comes into view, the discovery-doc
  caching strategy needs revisiting (per-tenant issuer, per-tenant
  discovery cache) — not first-slice.
- **Autolink-by-email default in prod**: the doc says `false` (fail-safe).
  Confirm at review — some ops folks prefer `true` for smoother onboarding
  when they know they control both the Authentik user list and the
  boomtime user list.
- **Registration endpoint fate under OIDC**: this doc assumes
  `/auth/register` 404s when `BOOM_AUTH_PROVIDER=oidc`. Alternative:
  redirect to Authentik's self-signup page (if enabled). Confirm at review.

---

## 10. Sub-bead plan (files as `bd create --parent=boom-0oe`)

Filed as separate `bd create` calls in the scoping-agent output — see the
scoping-agent report for the concrete IDs and titles. Rough shape below;
final IDs get stamped when the beads are created.

- **Substrate: schema + identity struct + flag-off compat shim**
  (first-slice — see §8)
- **Substrate: `IdentityResolver` interface + `LocalPasswordResolver`
  wrapper (no behavior change)**
- **Substrate: `SaveHeartbeatsRaw` variant + ingest-handler capability
  gates (feature-flagged off)**
- **Substrate: 70-site refactor of `resolveUser` → `h.identify(c)` +
  per-handler capability gates (split across 4-6 sub-beads: auth batch,
  curation/spaces batch, widgets/badges batch, stats/timeline batch,
  import/backup batch, dashboard/profile batch)**
- **Ops: `boomtime user set-role` / `boomtime user disable` / `boomtime
  user list` admin CLI (offline, no API exposure)**
- **Docs: `docs/design/user-model-and-oidc.md` finalized + `AGENTS.md`
  addendum on the flag rollout**
- **[deferred] OIDC resolver: `OIDCResolver` implementation against
  Authentik + oidc_sessions table + auth-callback route**
- **[deferred] OIDC resolver: dev docker-compose profile for Authentik +
  README section on first-time bootstrap**
- **[deferred] Quota enforcement: `capabilities.storage_quota_bytes`
  consulted by ingest + backup handlers**

Explicitly OUT of scope of the parent bead (own future beads, not this
scoping):
- SaaS billing tie-in for tier limits.
- Multi-tenant Authentik / per-tenant discovery cache.
- Boomtime AS an OIDC provider (issuing tokens other apps trust). Doable
  with the same substrate; large scope; file as its own epic when the need
  is real.
