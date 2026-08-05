# PROD OIDC (Authentik) wiring runbook — talos00-knowledgedump

Wire boomtime's OIDC login to the EXISTING in-cluster Authentik at
`https://authentik.knowledgedump.space`, using a **link-then-flip** migration so
no one is ever locked out.

Everything here is currently **INERT**: the new manifests exist but are
commented-out in `kustomization.yaml`, so ArgoCD's next sync changes nothing.
Activation is deliberate (step 4 below).

## Files in this overlay

| File | Role | Wired into kustomize? |
|------|------|-----------------------|
| `oidc-config.yaml` | strategic-merge patch: NON-SECRET `BOOM_OIDC_*` keys onto `boomtime-config` | commented-out in `patchesStrategicMerge` |
| `oidc-external-secret.yaml` | patch extending `boomtime-secrets` ExternalSecret with `BOOM_OIDC_CLIENT_SECRET` from 1Password | commented-out in `patchesStrategicMerge` |
| `authentik-blueprint-boomtime.yaml` | Authentik-side blueprint (provider + app + 3 groups) — applied to Authentik, **NOT** k8s | never (not a k8s manifest) |
| `OIDC-PROD-WIRING.md` | this runbook | n/a |

## Ordered activation steps

1. **Create the provider/app/groups in Authentik.** Apply
   `authentik-blueprint-boomtime.yaml` to the existing Authentik (see that
   file's header for the three apply methods: gitops blueprint dir, `kubectl cp`,
   or Admin UI import). Confirm `Applications → Providers → boomtime` and the
   groups `boomtime-admin` / `boomtime-full` / `boomtime-light` now exist.
2. **Copy the generated client_secret to 1Password.** Authentik generated a
   `client_secret` for the boomtime provider — copy it into vault item
   `boomtime`, field **`oidc-client-secret`**. (This is the only secret; it never
   goes in git.)
3. **Confirm the placeholders in `oidc-config.yaml`:**
   - `BOOM_OIDC_ISSUER` — copy the exact "OpenID Configuration Issuer" from the
     Authentik provider. **The trailing slash matters.**
   - `BOOM_OIDC_CLIENT_ID` — confirm it matches what Authentik assigned
     (blueprint pins `boomtime`; verify).
4. **Activate the manifests.** In `kustomization.yaml`, uncomment the two
   `patchesStrategicMerge` entries (`oidc-config.yaml` and
   `oidc-external-secret.yaml`). Commit to `main`.
5. **Deploy.** Let ArgoCD sync (or sync manually). The pod now has all
   `BOOM_OIDC_*` env vars AND `BOOM_OIDC_CLIENT_SECRET`, but still runs
   `BOOM_AUTH_PROVIDER=local` — password login is unchanged.
6. **Sign in locally** at `https://boomtime.knowledgedump.space` with your
   existing password account (your superuser).
7. **Link your superuser to Authentik** via the account-link flow
   (`/auth/link/oidc`). Make sure that Authentik identity is a member of
   `boomtime-admin`. Verify the link succeeds and the round-trip works
   (Authentik authorize → callback → linked).
8. **Flip to OIDC.** Change `BOOM_AUTH_PROVIDER` from `"local"` to `"oidc"` in
   `oidc-config.yaml`, commit, deploy. Sign in via "Continue with Authentik".
   Keep a local admin session open in another browser until you've confirmed the
   OIDC path fully works, so you can roll back the flip if needed.

Rollback at any point: revert the `oidc-config.yaml` / `kustomization.yaml`
change on `main`. Because the migration is link-then-flip, local auth keeps
working through steps 1–7.

## Split-horizon note

Prefer the **no-override** path: in prod the boomtime pod reaches Authentik at
the SAME public issuer URL the browser uses. When issuer == authorize == token
host, discovery + id_token `issuer` verification pass with **no**
`BOOM_OIDC_AUTHORIZE_URL` override — which is why that key is intentionally
omitted from `oidc-config.yaml`.

Fallback (only if a bring-up smoke test shows the pod cannot hairpin out to the
public `authentik.knowledgedump.space` host): mirror the dev overlay — keep the
`BOOM_OIDC_ISSUER` STRING equal to the public value (the id_token issuer must
match what Authentik stamps), point discovery at the internal Authentik service,
and set `BOOM_OIDC_AUTHORIZE_URL` to the PUBLIC authorize endpoint so the browser
redirect stays host-reachable. See the comment block in `oidc-config.yaml`.

## Security notes

- `BOOM_OIDC_AUTOLINK_EMAIL=false` — leave OFF. Linking on matching email alone
  is an account-takeover footgun; tracked HIGH finding **gaka-93f.12** (autolink
  is being removed). Linking is done deliberately in step 7 instead.
- `BOOM_OIDC_AUTOPROVISION=false` in prod — no auto-minted accounts during
  migration.
- No secret is ever committed. `BOOM_OIDC_CLIENT_SECRET` comes from 1Password at
  runtime via the ExternalSecret.

## OPEN QUESTIONS for the operator

1. **Real issuer URL?** Is `https://authentik.knowledgedump.space/application/o/boomtime/`
   the actual "OpenID Configuration Issuer" for the boomtime provider (exact
   host + trailing slash)? Confirm from Authentik and update `oidc-config.yaml`.
2. **How is your Authentik managed** — gitops/blueprint-dir or UI/manual? That
   decides which apply method in `authentik-blueprint-boomtime.yaml` you use, and
   whether the blueprint should live in Authentik's own gitops repo instead of
   here.
