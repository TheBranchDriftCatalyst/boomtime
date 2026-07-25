# OIDC Provider Survey for a GitOps-First Talos Homelab

**Status:** DRAFT — research artifact for bead `gaka-0oe.14`, appendix to `gaka-0oe`.
**Author:** Research agent, 2026-07-24.
**Do not commit** until user reviews.

---

## Executive Summary

**Stay on Authentik, but move from click-ops to blueprints + the official Terraform
provider — then re-evaluate in 12 months if the pain persists.** The user's stated
frustration is UI-driven config, not Authentik itself; blueprints (YAML applied by
the server, reconciled every 60 min, atomic rollback on error) plus the official
`goauthentik/authentik` Terraform provider close ~90% of that pain with zero
migration cost. Authentik is also the *most flexible* provider in this survey —
it's the only one that combines full-featured OIDC/SAML/LDAP-serve, forward-auth
outposts, upstream federation to Google/Apple/GitHub/SAML/OIDC/LDAP, *and* a
blueprint-based config-in-git story. **If you reject that** — because you want
*native Kubernetes CRDs* reconciled by argocd rather than "YAML applied by the
app" — the only alternative that keeps a fully-integrated identity product (not
assembled from parts) is **Zitadel** (official Terraform provider, Helm chart,
Apache-2). Everything else in this space either forces you to assemble your
own stack (Ory), depend on a community operator for the config layer
(Keycloak + Hostzero), or lose features you already rely on (Dex, Pocket ID,
Authelia — see notes).

**The one thing to tell the user:** *You do not have a wrong-provider problem;
you have a workflow problem. Adopt blueprints + Terraform on your existing
Authentik and the pain drops to near zero without you touching migration
risk.* If, after doing that, you still hate it — Zitadel is the only clean
switch target for a homelab.

---

## Comparison Matrix

Ratings: A (turnkey gitops) → F (unusable for gitops).
"Config-in-git" = *every* runtime setting (issuer, clients, users, groups, flows,
policies) can live in git and reach the server via a pull-loop or push-loop.

| Provider | GitOps deploy | Config-in-git | CRDs / Operator | Outpost/proxy story | Client provisioning | User/group provisioning | Migrate from Authentik | Community signal | Footprint | Learn-curve delta |
|---|---|---|---|---|---|---|---|---|---|---|
| **Authentik + blueprints + TF** | A (Helm) | **B+** (blueprints cover almost every model; drift semantics soft) | C (no native operator; #5675 open since 2023; blueprints self-reconcile server-side) | **F for user's pain** (forward-auth still needs proxy outposts) | A (blueprint or `authentik_provider_oauth2` + `authentik_application`) | A (blueprint / TF for users, groups, roles) | **N/A (stay put)** | B (Apache-2, active, ~14k stars) | ~500 MB RAM (server+worker+redis+db) | 0 |
| **Dex** | A (Helm, tiny chart) | C (staticClients in `config.yaml` / Helm values only; CRDs are state-only, NOT config) | D (CRDs exist but "Admins should not interact with these resources directly, except while debugging") | A (Dex is pure OIDC issuer, no proxy; pair with `oauth2-proxy` per-app if needed) | B (edit values.yaml, redeploy) | F (Dex has no local users; you *must* federate to upstream IdP — GitHub/LDAP/etc.) | High (Authentik users are local; Dex has none) | A (CNCF-sandbox, active) | ~50 MB | Medium (different model — Dex is a *federator*, not an IdP with local accounts) |
| **Zitadel** | A (official Helm) | **A** (official Terraform provider covers projects, apps, users, orgs, roles, IdPs, actions) | D (no native CRDs / operator; TF is the declarative surface) | A (pure OIDC/OAuth issuer, no outposts) | A (`zitadel_application_oidc` in TF) | A (TF resources for users, orgs, roles) | Medium (users must be re-created; export/import via TF import) | B (Apache-2, ~10k stars, corp-backed by Zitadel Cloud) | ~300 MB RAM + Postgres | Medium (multi-tenant model — orgs/projects/apps hierarchy is new vocab) |
| **Keycloak (official operator)** | A (Helm or operator) | **D** (official operator ships `Keycloak` + `KeycloakRealmImport` only; import is one-shot, "weak on day-2 drift and partial updates") | D (see above) | A (pure IdP, no outposts) | F via official operator (must go through Admin UI or realm re-import) | F same | High (realm re-import moves users but semantics differ) | A (CNCF-adjacent, Red Hat backed, huge) | 512 MB+ idle (Quarkus rewrite — much better than the WildFly era but still heavy) | High |
| **Keycloak + Hostzero community operator** | A | **B** (CRDs for Realm, Client, User, Role, Group, IdP, Component, Organization; drift detection claimed) | B (v1beta1, MIT, Keycloak 20–26+) | A | A (`KeycloakClient` CRD) | A (`KeycloakUser`, `KeycloakGroup`) | High | C (single sponsor "Hostzero," v1beta1 pre-1.0, small community) | 512 MB+ | High |
| **Ory Hydra + Kratos + Oathkeeper** | B (Helm charts for each) | C (Hydra: `hydra-maester` community CRD for OAuth2 clients only, alpha; Kratos: identity schemas in ConfigMap, no user CRDs; must assemble 3+ services) | C (partial — Hydra client CRD only, community-maintained, "core maintainers lack resources and time to actively maintain it") | A (Oathkeeper is the "outpost" but declarative via YAML) | B (via hydra-maester CRD) | F (no user CRDs) | Very high (architecturally different: three services replace one) | A (Apache-2, corp-backed by Ory) | ~200 MB combined idle | Very high |
| **Pocket ID** | A (single container) | B (yaml config, small surface) | F (no CRDs, no TF provider) | A (pure OIDC issuer) | C (via UI or REST) | C (via UI or LDAP sync) | High (feature loss: no SAML, no password auth, no LDAP federation) | C (~1 person project, growing fast) | ~256 MB | Low if user is happy going passkey-only |
| **Authelia** | A (Helm) | **A** (pure YAML, no UI at all) | F (no CRDs, no operator, no TF — YAML is the ONLY surface) | A (forward-auth via reverse proxy; no per-app outpost pod — proxy does it) | A (declared in `configuration.yml` `identity_providers.oidc.clients`) | B (users in `users_database.yml` OR LDAP federation) | Very high — **Authelia is not a full OIDC IdP the way Authentik is**; recent versions added OIDC-issuer as beta but SAML/LDAP-serve/self-service are missing | A (Apache-2, ~24k stars, active) | ~25 MB idle | Medium (different model — Authelia is a *gate*, not a full IdP) |
| **Okta / Auth0 / Google Workspace** | N/A (SaaS) | A (Terraform providers exist for all three) | N/A | A (pure OIDC) | A | A | High (cloud dependency violates homelab spirit) | A | 0 self-hosted | Low | Ruled out by user's homelab constraint |

**Legend for "outpost/proxy story":** *A* = OIDC-only, apps talk OIDC directly, no
per-app sidecar. *F* = per-app forward-auth proxy required. Only Authentik and
Ory Oathkeeper are actually in the "run a per-app proxy" business here — every
other provider on this list is a pure OIDC issuer and the *app* (or a shared
`oauth2-proxy`) speaks OIDC directly.

---

## Deep-dive per provider

### Authentik-with-blueprints (the "stay put" option)

**This is likely the answer.** The user's frustration is manual UI config, but
Authentik has had a first-class YAML config surface — *blueprints* — for years,
and most homelab users never learn it exists.

**What blueprints cover** (confirmed from official docs + community reference):
- `authentik_core.application`, `authentik_core.user`, `authentik_core.group`
- `authentik_providers_oauth2.oauth2provider`, `authentik_providers_saml.samlprovider`, `authentik_providers_proxy.proxyprovider`
- `authentik_flows.flow`, `authentik_stages_*` (all stage types)
- `authentik_policies.policybinding`
- `authentik_sources_oauth.oauthsource`, `authentik_sources_saml.samlsource`, `authentik_sources_plex.plexsource`
- `authentik_crypto.certificatekeypair`
- `authentik_outposts.outpost`
- `authentik_providers_oauth2.scopemapping` (property mappings)
- `authentik_rbac.role`

**Reconciliation model:**
- File-based blueprints: reapplied every 60 min, plus immediate on file change (fsnotify).
- OCI blueprints: pulled from a registry, same 60-min cadence.
- Blueprints applied atomically — one bad entry rolls back the whole file.
- **Weakness:** blueprints are additive/upsert. If you `kubectl delete` a blueprint, the *resources it created remain* — this is not argocd-style prune. You must manage lifecycle explicitly if you delete a client.

**Real-world declaration** (a working blueprint for boomtime, extrapolated from
community examples):

```yaml
version: 1
metadata:
  name: boomtime-oidc
entries:
  - identifiers: { name: boomtime-oauth }
    model: authentik_providers_oauth2.oauth2provider
    attrs:
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      client_type: confidential
      client_id: boomtime
      redirect_uris:
        - https://boomtime.example.com/oidc/callback
      signing_key: !Find [authentik_crypto.certificatekeypair, [name, authentik Self-signed Certificate]]
      property_mappings:
        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, openid]]
        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, email]]
        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, profile]]
  - identifiers: { slug: boomtime }
    model: authentik_core.application
    attrs:
      name: Boomtime
      provider: !KeyOf boomtime-oauth
```

That is *one PR* to add boomtime. No UI clicks. Ship it via argocd by mounting
blueprints from a ConfigMap into the authentik `server` pod at `/blueprints/`
and Authentik picks them up automatically.

**Where blueprints leave gaps** (be honest):
1. **Outposts.** Blueprints can *declare* an outpost, but the Kubernetes-integration outpost still runs as a sidecar/deployment. Forward-auth remains the fundamental architecture — if the user's core pain is "every app that needs auth needs a proxy," blueprints don't fix that. That pain is Authentik's *design*, not its config surface.
2. **Drift detection.** Blueprints upsert but do not delete orphaned resources. If you rename an app in git, the old one lingers unless you manually delete it (or use the Terraform provider, which does have destroy semantics).
3. **No native Kubernetes CRD.** The GitHub issue for `authentik-operator` (#5675) has been open since May 2023 with "enhancement/confirmed" but no owner, no PR, no timeline. Do not wait for it.

**The Terraform provider** (`goauthentik/authentik`) fills the "argocd doesn't know
these resources exist" gap for the ~10% of users who want true declarative-with-diff:
- Official, Apache-2, moderate maturity (~133 stars, 1,345 commits, versioned against Authentik releases like `2026.4.0`).
- Resources: `authentik_application`, `authentik_provider_oauth2`, `authentik_provider_saml`, `authentik_provider_proxy`, `authentik_user`, `authentik_group`, `authentik_flow`, `authentik_stage_*`, `authentik_policy_*`, `authentik_source_*`, `authentik_outpost`, `authentik_scope_mapping`.
- Run it from argocd via a Terraform-controller pattern (Flamingo, tf-controller) *or* run it in CI on PR-merge — both are common patterns.

**Migration cost from status quo: zero.** You already have Authentik. Adopting
blueprints is additive: existing UI-created resources continue to work; new
resources go in git.

### Dex

**Purpose:** the k8s-native OIDC *federator*. Its job is to take upstream
identity (GitHub, LDAP, another OIDC) and issue OIDC tokens your apps can
consume — *it has no local user database*. This is a fundamental fit mismatch
with Authentik's model where users are stored in the identity provider.

**GitOps story:**
- **Deploy:** clean Helm chart, tiny (~50 MB RAM).
- **Config:** everything in `config.yaml` (mounted as ConfigMap or Helm values). `staticClients` block declares OAuth clients. `connectors` block declares upstream IdPs.
- **CRDs:** Dex *stores state* in CRDs (auth codes, refresh tokens, offline sessions) but explicitly warns "Admins should not interact with these resources directly." **You cannot define an OAuth client via a CRD.** You edit the ConfigMap and re-roll the deployment.

**Client provisioning:** PR that edits `values.yaml`, argocd reconciles the
Helm release, Dex re-reads its config. Works, but is not "true k8s-native
declarative" — it's "declarative-via-Helm-values."

**When to choose Dex:** you already federate everything to GitHub / LDAP / your
company IdP and just need an OIDC front-end for k8s workloads. Common pairing:
Dex + `oauth2-proxy` in front of every app. This is essentially what argocd's
own dex integration does.

**Why NOT for boomtime:** you'd have to also stand up an *upstream* identity
source (LDAP, another OIDC, GitHub org). Authentik does both jobs in one
process; Dex does neither by itself.

### Zitadel

**Purpose:** modern IAM built as a full identity product (users, orgs, projects,
apps, roles, IdPs), API-first, Go (not Rust — a common misconception; Rust
lineage refers to earlier ZITADEL versions).

**GitOps story:**
- **Deploy:** official Helm chart at `charts.zitadel.com`, includes optional Postgres subchart, `helm install` deploys the stack.
- **Config:** ConfigMaps for infra settings, secrets for masterkey/db creds.
- **Declarative resource management:** **the official Terraform provider is the answer** (`zitadel/terraform-provider-zitadel`, Apache-2, ~61 stars, 721 commits, official). Resources include: `zitadel_project`, `zitadel_application_oidc`, `zitadel_application_api`, `zitadel_user_human`, `zitadel_user_machine`, `zitadel_org`, `zitadel_org_member`, `zitadel_org_idp_oidc`, `zitadel_action`, `zitadel_project_role`, etc.
- **CRDs / operator:** *no*. There was an older `zitadelctl` operator, but the current push is Helm + Terraform.
- **Actions gap:** JavaScript "Actions" for extension logic are stored server-side; there's an [open FR for making them declarative-via-config](https://github.com/zitadel/zitadel/issues/9803). Small gap.

**Client provisioning:** `zitadel_application_oidc` resource in TF, apply via
tf-controller / Flamingo / CI. Clean.

**User/group provisioning:** full TF resources for `user_human`, `user_machine`,
`org_member`. You can also federate to upstream IdPs via `org_idp_oidc`, but
unlike Dex, Zitadel *also* has native local users.

**Migration cost from Authentik:** medium — you'd re-declare users in TF (or
federate to Authentik-as-IdP during a transition), re-register every client
app, and re-do groups → project-role semantics (Zitadel doesn't have flat
"groups" — it has projects and roles inside them, which is arguably a cleaner
model but is *not* a drop-in).

**When to choose Zitadel:** you want a full identity product like Authentik but
with a first-class terraform surface and *no* forward-auth outpost complexity
(apps speak OIDC directly, no proxy required).

**Weakness:** newer, smaller community than Authentik/Keycloak. The "actions"
JS runtime is powerful but is the one non-declarative piece.

### Keycloak (both flavors)

**Official operator** (`keycloak-operator`, from keycloak.org, Quarkus, current):
- CRDs: `Keycloak` (server instance) + `KeycloakRealmImport` (one-shot realm import from a JSON dump).
- **This is not enough for gitops.** `KeycloakRealmImport` is import-oriented, "weak on day-2 drift and partial updates" (per the community post). If you edit the CR to change a client, behavior is fuzzy — some fields update, some don't, drift is not detected against the live Keycloak state.
- No CRDs for `Client`, `User`, `Group`, `IdentityProvider` in the official operator. There has been "expected in Keycloak 21" language for years and it hasn't materialized.

**Community operator (Hostzero GmbH `keycloak-operator`):**
- Full CRDs: `KeycloakInstance` / `ClusterKeycloakInstance`, `KeycloakRealm`, `KeycloakClient`, `KeycloakUser`, `KeycloakUserCredential`, `KeycloakRole`, `KeycloakRoleMapping`, `KeycloakIdentityProvider`, `KeycloakGroup`, `KeycloakComponent`, `KeycloakOrganization`.
- Claims drift detection, automatic client secret sync to k8s Secrets, MIT license, Keycloak 20–26+.
- **Downsides:** v1beta1 (pre-1.0), single-sponsor (Hostzero GmbH), you're betting on one small vendor to keep it alive.

**Krateo KOG (alternative approach):** generate CRDs from Keycloak's OpenAPI
spec. Interesting but experimental — you're now betting on both Keycloak and
Krateo.

**When to choose Keycloak:** you're an enterprise, you want SAML/OIDC/CIBA/UMA
and the biggest feature surface in the space, you're OK with 512 MB+ idle
footprint, and you're willing to bet on the Hostzero operator (or write your
own realm-JSON export/import CI). **For a homelab, this is overkill.**

### Ory Hydra + Kratos + Oathkeeper

**Architecture:** three services, one job each. Hydra = OAuth2/OIDC token
issuer, no users. Kratos = identity/user management, no tokens. Oathkeeper =
policy enforcement / forward-auth. You assemble them.

**GitOps story:**
- Helm charts for each (`ory/hydra`, `ory/kratos`, `ory/oathkeeper`).
- Hydra clients: managed by `hydra-maester` — a community-maintained (⚠ explicitly *not* an official Ory project, "core maintainers lack resources") k8s controller with CRD `oauth2clients.hydra.ory.sh/v1alpha1`. Alpha.
- Kratos identities: identity schemas defined in ConfigMap; individual identities managed via API/CLI. **No user CRDs.**
- Oathkeeper rules: declarative via YAML rules file.

**When to choose Ory:** you're building a *product*, need extreme
customization, have a full-time platform team, and the composability outweighs
the assembly cost. **Very much not homelab territory.**

### Authelia

**Purpose:** lightweight authentication + authorization gate designed to sit
in front of reverse-proxied apps (nginx / Traefik / HAProxy / Caddy) via
forward-auth. Was historically *not* an OIDC issuer at all — you couldn't use
it as an OIDC provider for a modern app like boomtime. In recent releases
(v4.38+) Authelia has added **OIDC 1.0 Provider** support (currently marked
beta-stable), which changes the calculus meaningfully.

**Why the user is right that this is worth a serious look:**
- **Fully YAML-native.** No admin UI at all — literally *everything* is
  declared in `configuration.yml`: OIDC clients, users, groups, access-control
  rules, session config, 2FA, WebAuthn, TOTP, notifiers. Edit YAML, restart
  the pod, done. This is the purest declarative-config story in the entire
  survey — even purer than Zitadel (which has YAML for infra + TF for
  resources).
- **Tiny footprint** — ~25 MB RAM idle. Ten to twenty times smaller than
  Authentik.
- **Argocd-friendly.** Because config is one ConfigMap + one Secret, argocd
  reconciles it natively. No CRDs needed *because there's no separate
  resource layer* — the whole app is stateless config-plus-a-user-db.

**What Authelia is NOT — and this matters for boomtime:**
- **No LDAP-serve.** Authelia can *consume* LDAP (as a user source) but does
  not serve LDAP.
- **No SAML issuer.** OIDC and forward-auth only.
- **No user self-service.** No user portal for password reset, profile edit,
  MFA enrollment beyond first-time TOTP. If a homelab user forgets a
  password, you edit `users_database.yml` (which stores Argon2 hashes) or
  federate to LDAP/AD and manage there.
- **No SCIM, no user provisioning API, no import from CSV.**
- **OIDC Provider is beta.** The feature works and is widely used in the
  community for homelab OIDC integrations, but it's explicitly labeled
  "beta stable" — Authelia's docs say it *"should be safe to use in
  production for OIDC 1.0 relying party clients but the implementation may
  contain undiscovered bugs and may still make small breaking changes."*
- **No federated *upstream* IdP for OIDC login.** Authelia doesn't let a user
  "sign in with Google" to Authelia the way Authentik does — Authelia is the
  authenticator; it doesn't delegate authentication to upstream OIDC/social
  providers. (You *can* federate through LDAP/AD, but not through OIDC/social.)

**Client provisioning (boomtime style):**
```yaml
identity_providers:
  oidc:
    clients:
      - client_id: boomtime
        client_name: Boomtime
        client_secret: '$argon2id$v=19$...'  # hashed
        public: false
        redirect_uris:
          - https://boomtime.example.com/oidc/callback
        scopes: [openid, email, profile, groups]
        userinfo_signed_response_alg: none
```
One PR, restart the Authelia pod, boomtime works. Very clean.

**When to choose Authelia:**
- Your homelab is mostly reverse-proxied apps behind Traefik/nginx.
- You want the *simplest possible* gitops story: one YAML file is the entire IdP.
- You don't need SAML, don't need LDAP-serve, don't need user self-service, don't need to sign in to Authelia *with* Google/GitHub.
- You are willing to run OIDC Provider in its current "beta stable" state.
- You want to drop from Authentik's ~500 MB / 4-container footprint to a single ~25 MB pod.

**When NOT to choose Authelia:**
- You have any SAML-only app (many enterprise apps).
- You need users to sign in to *your* IdP using upstream Google / Apple / GitHub identities (see next section — Authentik does this, Authelia does not).
- You need an LDAP directory service (not a consumer).
- You want a user self-service portal.

**Migration from Authentik:** medium-hard. You lose the "Authentik as
federating hub for Google/Apple/etc" model (see next section — this is the
user's second question and it's a big deal). Users would need to be re-created
in `users_database.yml` (Argon2 hashes) or you'd stand up an LDAP behind
Authelia and migrate identity into that.

### Pocket ID

**Purpose:** lightweight OIDC issuer for homelab, passkey-only. Single
container, ~256 MB RAM idle.

**GitOps story:** small yaml config surface, no CRDs, no TF provider, admin
things happen in the UI or REST API. Not really designed for "clients-in-git"
workflow.

**Deal-breakers for boomtime:** no SAML, no password auth, no LDAP federation.
Every user must have a passkey, and you must add users via the UI or REST.

**When to choose Pocket ID:** small homelab, everyone has a YubiKey, you don't
care about declarative config, you value the tiny footprint. Not the user's
profile.

### Authentik as a federating hub (the "delegate to Google/Apple/GitHub" pattern)

**The user asked: "can Authentik proxy/delegate to Google, Apple, etc.?" — YES,
this is one of Authentik's biggest strategic advantages** and is one of the
main reasons to stay put.

Authentik supports "federated identity providers" (aka social login sources)
where an upstream OIDC/OAuth2/SAML provider is the actual authenticator, and
Authentik federates the identity into its local user database. Documented
built-in sources:
- **OAuth 2.0 social:** Google, Apple, GitHub, Discord, Facebook, Twitter/X,
  Twitch, Azure AD, Mailcow, and any generic OAuth2 provider.
- **OIDC:** any generic OIDC IdP (including another Authentik, Zitadel, Keycloak, Okta, Auth0, etc.).
- **SAML:** any SAML 2.0 IdP.
- **LDAP / Active Directory / FreeIPA** (as a source, and Authentik can also
  serve LDAP downstream).
- **Kerberos**.
- **Plex, Mailcow, Apple, etc.** — long tail.

**Why this matters for a homelab:**
- You can let *yourself* sign in via Google (no local password), while boomtime
  and other apps see Authentik as their OIDC provider — Authentik brokers the
  Google identity into a stable local user record.
- You can let a friend sign in with their Apple ID for one app without ever
  creating a local password.
- You can require MFA in Authentik on top of Google's own MFA (belt-and-braces).
- You can mix: some users are local, some come from Google, some from LDAP —
  Authentik unifies them into one identity/group model that boomtime consumes.

**This is a genuine differentiator:**
- ✅ **Authentik** — native OAuth/OIDC/SAML/LDAP source federation, declarable in blueprints (`authentik_sources_oauth.oauthsource`).
- ✅ **Keycloak** — full IdP federation (OIDC, SAML, social).
- ✅ **Zitadel** — OIDC IdP federation via `zitadel_org_idp_oidc`.
- ⚠️  **Dex** — is *only* a federator (that's its whole purpose) — but has no local user layer, so identities live upstream.
- ⚠️  **Ory Kratos** — supports OIDC/social connectors.
- ❌ **Authelia** — **does NOT support upstream OIDC/social login.** You cannot sign in to Authelia with your Google account. LDAP-source only.
- ❌ **Pocket ID** — passkey-only, no upstream IdP federation.

**Blueprint example for adding Google as a login source in Authentik:**

```yaml
version: 1
entries:
  - identifiers: { slug: google }
    model: authentik_sources_oauth.oauthsource
    attrs:
      name: Google
      provider_type: google
      consumer_key: "GOOGLE_CLIENT_ID"
      consumer_secret: !Env GOOGLE_CLIENT_SECRET
      enrollment_flow: !Find [authentik_flows.flow, [slug, default-source-enrollment]]
      authentication_flow: !Find [authentik_flows.flow, [slug, default-source-authentication]]
```

Now "Sign in with Google" appears on the Authentik login page, next to
username/password, and consumed users are automatically enrolled with the
Authentik user model that boomtime sees.

**Verdict:** if federating to Google/Apple/GitHub/etc. is a workflow you rely
on (or want to rely on), **Authentik and Keycloak are the only two providers
in this survey that do it well.** Zitadel does OIDC federation but the social
UX is more DIY. Authelia does not do it at all — this alone may rule out
Authelia for the user's setup.

### Cloud (Okta, Auth0, Google Workspace)

**Ruled out per user constraint** (homelab-first). Included for completeness:
all three have mature Terraform providers that give truly turnkey declarative
config, at the cost of an external dependency, per-MAU pricing, and vendor
lock-in. Not recommended.

---

## Authentik-with-blueprints deep dive (the case for staying put)

**The user's problem statement:** "I hate that outposts, providers, and per-app
registration are all clicked in the UI."

**What blueprints solve:**
- ✅ Providers (OAuth2 / SAML / Proxy) — full YAML.
- ✅ Applications — full YAML.
- ✅ Users, groups, roles — full YAML.
- ✅ Flows, stages, policies — full YAML.
- ✅ Certificates and property mappings — full YAML.
- ✅ Outpost *definitions* — full YAML (`authentik_outposts.outpost`).
- ✅ Reconciliation loop — 60-min pull, plus fsnotify on file change.
- ✅ Atomic apply — bad entry rolls back the whole file.

**What blueprints do NOT solve:**
- ❌ **The forward-auth outpost is still a running pod that proxies every request.** For OIDC-native apps (Grafana, argocd, boomtime), you don't need the proxy outpost at all — those apps speak OIDC and you just point them at Authentik's `/application/o/*` endpoints. For non-OIDC apps (Jellyfin admin, Sonarr, anything with only basic-auth), you *do* need the proxy outpost. If your homelab is 80% legacy apps behind forward-auth, blueprints don't reduce that pain — but *no OIDC provider does*, because the pain is "the app doesn't speak OIDC." (`oauth2-proxy` in front of Dex/Zitadel/Keycloak solves the same problem the same way.)
- ❌ **No prune-on-delete.** Removing a blueprint file does not remove the resources it created. Use the Terraform provider for that.
- ❌ **No native k8s CRD.** argocd doesn't "see" your Authentik state — it sees the ConfigMap containing the blueprint YAML, and trusts Authentik's own reconciler. If you want argocd-side sync-status for individual clients, use the Terraform provider with tf-controller.

**How much of the pain goes away:** if the user's frustration is *"I click
through the UI to add every new app"*, adopting blueprints removes ~100% of
that specific pain. If the frustration is *"there's a forward-auth proxy pod
per proxy provider,"* that pain is architectural, and switching to Dex or
Zitadel or Keycloak doesn't fix it — you'd just move to `oauth2-proxy` per
app, which is the same architecture.

**Recommended migration inside Authentik (no provider change):**

1. Export current UI-configured state via the Authentik API to see what
   blueprints look like for your setup (there's a `bp export` mgmt command).
2. Commit that export to your git repo as your first blueprint.
3. Mount the blueprint dir into the `server` Deployment via ConfigMap volume at
   `/blueprints/`.
4. From now on, every new app is a PR editing that ConfigMap.
5. **Optional:** stand up the Terraform provider in CI or via tf-controller
   for resources where you need prune-on-delete and argocd-visible drift.

**Estimated effort:** one afternoon. Zero downtime. Zero data migration.

---

## Recommendation

### #1 — Stay on Authentik; adopt blueprints + Terraform provider

**Rationale (2 sentences):** The user's stated pain is UI-driven config, and
that pain is fully addressable inside the current provider via blueprints
(atomic, self-reconciling YAML) plus the official `goauthentik/authentik`
Terraform provider for resource lifecycle — with zero migration cost, zero
downtime, and no risk of losing features. Switching providers to solve a
workflow problem trades a known Small Pain for an unknown Large Pain
(re-registering every app, re-creating every user, re-learning the model).

**Pros:**
- Zero migration risk / data loss.
- Blueprints cover ~all runtime resources.
- Terraform provider covers the ~10% blueprints miss (prune semantics, argocd
  visibility).
- Authentik is Apache-2, has ~14k stars, active development, first-class
  SAML/OIDC/LDAP/Proxy, mature outpost story.
- Existing user knowledge preserved.

**Cons:**
- No native k8s CRD — reconciliation lives inside the Authentik server, not in
  argocd. If the user's mental model is "argocd is the single source of truth
  for every resource," blueprints will feel like a workaround.
- Forward-auth outposts still exist (but this is *architectural*, not solvable
  by switching providers — see above).
- Community operator not on the horizon (#5675 has languished since 2023).

### #2 — Switch to Zitadel

**Rationale (2 sentences):** If the user does move to blueprints, dislikes the
"config lives in a ConfigMap the app reads" model, and wants a *truly*
declarative-with-diff surface where the Terraform state file is the source of
truth for every OIDC resource, Zitadel is the only alternative that keeps a
fully-integrated identity product (users, orgs, projects, apps, roles, IdPs
all in one place) with a first-class, official Terraform provider. It's also
the only alternative that dispenses entirely with the forward-auth outpost
concept — apps speak OIDC directly, no proxy pod required.

**Pros:**
- Official Terraform provider, Apache-2, actively maintained.
- Modern architecture (multi-tenant orgs/projects).
- No outpost complexity — pure OIDC issuer.
- Postgres-only, simple ops.
- Corporate backing (Zitadel Cloud upstream) reduces bus factor risk.

**Cons:**
- Real migration cost (re-register every app, re-declare every user).
- Smaller community than Authentik.
- No SAML (as of last check — verify current state; SAML support was on
  roadmap).
- Actions (extensibility hooks) not fully declarative yet.
- Learning curve: orgs/projects/apps hierarchy is new vocab.

### Why NOT the others (top-line)

- **Authelia:** the *purest* declarative-config story (one YAML file *is* the
  entire IdP) and the tiniest footprint (~25 MB), but two dealbreakers: (1)
  **does not federate to upstream OIDC/social providers** — you can't sign
  in to Authelia with Google or Apple, only via local users or LDAP; (2)
  OIDC Provider support is beta and lacks SAML, SCIM, user self-service, and
  LDAP-serve. Great for pure forward-auth homelabs, wrong for boomtime's
  "let me sign in with Google and have boomtime see my identity" pattern.
- **Dex:** no local users. You'd need to also stand up LDAP/GitHub/etc. as the
  actual identity source. Wrong shape.
- **Keycloak (official operator):** no client/user CRDs. Weak drift on
  RealmImport. Not gitops-native yet.
- **Keycloak (Hostzero operator):** technically better fit than the official
  one, but single-vendor v1beta1 bet, and 512 MB+ idle for a homelab is heavy.
- **Ory:** three services to run and assemble. Too much for a homelab. `hydra-maester`
  is community-alpha and covers only clients.
- **Pocket ID:** no SAML, no LDAP, passkey-only. Feature-loss migration.
- **Cloud (Okta/Auth0):** violates homelab constraint.

---

## Migration cost estimate

Costs are ROM estimates for a small homelab (~10 apps, ~5 users, ~3 groups).

| Target | New infra | Config re-declare | Data migration | Downtime | Rollback path | Total effort |
|---|---|---|---|---|---|---|
| **Authentik + blueprints (stay)** | 0 | 1 blueprint per app (10 total, ~30 LOC each) | 0 (existing DB stays) | 0 | trivial (revert PR) | **1 afternoon** |
| **Authentik + TF provider (stay)** | tf-controller (optional) OR CI pipeline | 1 TF resource per app + provider + user | 0 (import existing state via `terraform import`) | 0 | trivial | **1-2 days** |
| **Zitadel** | Helm release + Postgres + argocd app | rewrite every app registration in TF; users re-created | Users: manual re-invite (no export/import). Sessions lost. | ~1 hr (cutover per app; DNS flip; test) | painful (need to re-point every app back) | **1-2 weeks** |
| **Keycloak + Hostzero op** | Helm + operator + Postgres | one CRD per realm/client/user; realm import for bulk | Realm JSON export from Authentik? No, formats differ — manual | ~1 hr per app | painful | **1-2 weeks** |
| **Ory (Hydra + Kratos + Oathkeeper)** | 3 Helm charts + Postgres + maester + policy rules | invent your own identity schemas; migrate users to Kratos schema; every client via CRD; every route through Oathkeeper | Manual export/reimport, semantics differ | ~1 day per app | very painful | **2-4 weeks** |
| **Authelia** | Helm + Redis (session store) + reverse-proxy forward-auth | one YAML file: OIDC clients + users + ACLs; ~15 LOC per app | Users re-created in `users_database.yml` (Argon2 hashes) or stand up LDAP; no import from Authentik | ~1 hr per app | moderate (config revert + re-point OIDC) | **3-5 days** — but you lose Google/Apple/GitHub federation |
| **Dex** | Helm + oauth2-proxy per app + external IdP (GitHub/LDAP) | staticClients in values.yaml | Users don't exist locally — must federate | ~30 min per app | moderate | **1 week** |
| **Pocket ID** | Single container | Add users via UI/REST; register apps via UI | Users must enroll passkeys | ~30 min per user | moderate | **1 week + user coordination** |

---

## Open questions the user needs to answer

1. **Is your pain "UI clicks" or "outpost pods"?** They are separate problems.
   Blueprints solve #1 completely; #2 is architectural and follows you to any
   provider (via `oauth2-proxy`) whenever an app doesn't speak OIDC natively.

2. **Do you actually use SAML, LDAP-serve, or Proxy providers?** If Authentik-as-
   LDAP-server or Authentik-as-SAML-IdP is in your stack, Zitadel is a
   regression (verify current SAML support before switching). Dex, Pocket ID,
   and Ory Hydra also lack SAML/LDAP-serve.

3. **Do you already run Terraform anywhere in this homelab?** If yes,
   `goauthentik/authentik` TF provider is a natural add. If no, the tf-controller
   / Flamingo learning curve may make blueprints (pure YAML) the better first
   step.

4. **Are you willing to bet on the Authentik #5675 operator ever shipping?**
   If no (recommend no — 3 years, no progress), blueprints + TF is the durable
   answer.

5. **Do you rely on (or want to rely on) "sign in with Google/Apple/GitHub"?**
   If yes, this rules out Authelia, Pocket ID, and pure-Dex (Dex only does
   federation, no local model). Authentik, Keycloak, and Zitadel are the
   only survivors — with Authentik being the most turnkey (built-in social
   sources for Google, Apple, GitHub, Azure, Discord, Twitter, Twitch,
   Facebook, and generic OAuth2/OIDC/SAML).

6. **How many apps behind forward-auth vs how many OIDC-native?** If 80%+ are
   OIDC-native (Grafana, argocd, boomtime, k8s dashboard, etc.), the outpost
   pain is limited to a small tail and any provider works. If 80%+ need
   forward-auth (Sonarr, Radarr, Jellyfin admin, Pi-hole admin), then
   Authentik's proxy outpost is actually *saving* you from wiring up
   `oauth2-proxy` per app.

---

## References the user should read personally

- **Authentik blueprints reference (models):**
  https://docs.goauthentik.io/customize/blueprints/v1/models/
- **Authentik system OAuth2 provider blueprint (real example):**
  https://github.com/goauthentik/authentik/blob/main/blueprints/system/providers-oauth2.yaml
- **Authentik discussion — deploy default OAuth2 provider + application via blueprints:**
  https://github.com/goauthentik/authentik/discussions/17550
- **Community walk-through (best real-world blueprint guide found):**
  https://uis.sovereignsky.no/docs/services/identity/blueprints-syntax
- **Managing Authentik with Terraform (Tim Van Wassenhove, 2025):**
  https://timvw.be/2025/03/18/managing-authentik-with-terraform/
- **Authentik operator issue (currently stalled since May 2023):**
  https://github.com/goauthentik/authentik/issues/5675
- **Zitadel Terraform provider (official):**
  https://github.com/zitadel/terraform-provider-zitadel
- **Zitadel Terraform provider docs:**
  https://zitadel.com/docs/guides/manage/terraform-provider
- **GitOps-Native Keycloak (Diego Braga, why the official operator falls short):**
  https://medium.com/@diego.braga86/gitops-native-keycloak-realms-clients-and-federation-as-kubernetes-crs-with-zero-operator-code-19cfc47d6af3
- **Hostzero community keycloak-operator (fills the official operator's config gap):**
  https://github.com/Hostzero-GmbH/keycloak-operator
- **hydra-maester (community, alpha, OAuth2 clients only):**
  https://github.com/ory/hydra-maester
- **Dex config storage docs (why CRDs are not for config):**
  https://dexidp.io/docs/storage/
- **Pocket ID (lightweight passkey-first OIDC):**
  https://github.com/pocket-id/pocket-id
- **Authelia OIDC Provider docs (config-file spec for OIDC clients, beta stable):**
  https://www.authelia.com/configuration/identity-providers/openid-connect/provider/
- **Authelia vs Authentik (Stonegarden, 2025 — good comparison of both models):**
  https://blog.stonegarden.dev/articles/2025/06/authelia-oidc/
- **Authentik federated identity providers (Google/Apple/GitHub sources):**
  https://docs.goauthentik.io/users-sources/sources/social-logins/
- **Authentik "Sign in with Apple" source docs:**
  https://docs.goauthentik.io/users-sources/sources/social-logins/apple/
- **Authentik "Sign in with Google" source docs:**
  https://docs.goauthentik.io/users-sources/sources/social-logins/google/

---

## Dimensions I could NOT confidently answer

- **Authentik drift-detection semantics** for blueprints (docs describe atomic
  apply but not "what happens if the live DB was hand-edited after a blueprint
  was applied"). Suspect blueprints re-assert declared state every 60 min but
  the docs are quiet on partial-field drift.
- **Zitadel current SAML support state.** Zitadel is historically OIDC/OAuth2-
  first; SAML was on roadmap for a long time. If the user needs SAML-IdP,
  verify current state before switching.
- **Hostzero keycloak-operator production usage in the wild** — GitHub project
  activity is real but I didn't find independent case studies. Treat as
  "promising v1beta1" not "proven in production."
- **Ory `hydra-maester` future.** Maintainers explicitly note lack of time.
  Assume it may go unmaintained; do not build on it for anything durable.
- **Whether the Authentik `#5675` operator will ever ship.** Open since May
  2023, no assignee, no branch, no PR. My best guess is *no*, but this is a
  guess.
