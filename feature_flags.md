# boomtime — Feature Flags 🎛️

Every switch that changes what boomtime **does** — server env vars, frontend URL params,
and browser-storage gates — with its default, where it's read, and **what it is set to on
the deployed instance** (`k8s/overlays/talos00-knowledgedump`, i.e.
`boomtime.knowledgedump.space` / `boomtime.talos00`).

## How to read the tables

| Column | Meaning |
|---|---|
| **Default** | what the server does when the var is **unset** |
| **Prod** | ✅ explicitly set in the overlay · **—** unset, so the Default applies |
| **Read at** | `internal/config/config.go` unless another path is given |

**Truthiness** — `getEnvBool` (`config.go:364`) accepts `1` `true` `yes` `on` and
`0` `false` `no` `off`, case-insensitive and trimmed. **Anything else falls back to the
default**, silently — `BOOM_FEATURE_BOOKS=enabled` is *off*, not on. That's why the overlay
can write `BOOM_FEATURE_LABEL_IMAGES: "on"` and get `true`.

Line numbers are a convenience, not a contract — grep the flag name.

---

## 🚩 Feature flags — `BOOM_FEATURE_*`

The master switches. All default **off**, so a fresh instance ships inert and an operator
opts in.

| Flag | What it gates | Default | Prod | Read at |
|---|---|---|---|---|
| `BOOM_FEATURE_GITHUB_STATS` | Per-user GitHub connect + the `github-stats-refresh` job. Advertised to the FE as `github_connect_enabled` **only when the OAuth creds are also present** | `false` | ✅ `true` | `:426` |
| `BOOM_FEATURE_BOOKS` | catalyst-books / audiobooks domains + the shared Connect-Amazon settings surface. FE hides the whole card when false | `false` | ✅ `true` | `:427` |
| `BOOM_FEATURE_LABEL_IMAGES` | Label-artwork generation (ComfyUI worker + the `label-image` job kind) | `false` | ✅ `on` | `:457` |
| `BOOM_FEATURE_USER_MODEL` | The user-demarcation substrate. **Off = every request gets an all-capability identity** and no gate ever fires. On = real roles/capabilities, fails closed on disabled accounts | `false` | ✅ `true` | `:436` |
| `BOOM_FEATURE_ADMIN_CLI` | The admin CLI-runner HTTP surface (`/api/v1/admin/cli/*`). When off the routes are never registered, so they 404 like any unknown path | `false` | ✅ `true` | `:438` |
| `BOOM_FEATURE_ROLLUP_SKIP` | Lets ingest skip the rollup/gap machinery for identities lacking `CapGenerateRollups`. **No effect unless `USER_MODEL` is also on** | `false` | — off | `:437` |
| `BOOM_FEATURE_BILLING` | The Stripe SaaS surface (pricing page, upgrade CTA, billing settings). Not shipped yet | `false` | — off | `:434` |

---

## 🔐 Auth, registration & OIDC

| Flag | What it gates | Default | Prod | Read at |
|---|---|---|---|---|
| `BOOM_AUTH_PROVIDER` | `local` (username+password) or `oidc` (Authentik). The FE swaps the login form and signup CTA on this | `local` | ✅ `oidc` | `:413` |
| `BOOM_ENABLE_REGISTRATION` | Whether `POST /auth/register` accepts new users. Moot under `oidc` (signup flows through Authentik) but still reported | `true` | ✅ `false` | `:404` |
| `BOOM_BETA_USER_REGISTRATION` | **Server kill switch for the beta onboarding preview** — see [Frontend gates](#-frontend-gates--url-params--browser-storage). Set `false` to disable the preview instance-wide regardless of the URL flag | `true` | — **on** | `:435` |
| `BOOM_ADMIN_USERS` | Comma-separated admin allowlist (`requireAdmin`). Double-gated with `CapAdmin` route middleware | `""` | ✅ `panda` | `:461` |
| `BOOM_SESSION_EXPIRY` | Access-token lifetime, hours | `24` | ✅ `24` | `:405` |
| `BOOM_COOKIE_SECURE` | `Secure` on the refresh cookie. **Default derives from `BOOM_ENV`** (true when prod/production) | *env-derived* | — true | `:470` |
| `BOOM_CORS_ALLOWED_ORIGINS` | CORS allowlist. **Required when `BOOM_ENV=prod`** — the server refuses to boot without it rather than guess | `""` | ✅ `https://boomtime.knowledgedump.space` | `cmd/boomtime/main.go:199` |
| `BOOM_ENCRYPTION_KEY` | AES-256-GCM key for every per-user secret (wakatime key, GitHub token, Amazon device, Hardcover key). Without it those columns can't be read or written | *unset* | ✅ via `boomtime-encryption-key` Secret | `internal/auth/crypto.go` |

**OIDC wiring** — all inert unless `BOOM_AUTH_PROVIDER=oidc`:

| Flag | What it gates | Default | Prod | Read at |
|---|---|---|---|---|
| `BOOM_OIDC_ISSUER` | Discovery URL | `""` | ✅ `https://auth.knowledgedump.space/application/o/boomtime/` | `:414` |
| `BOOM_OIDC_CLIENT_ID` | Client id | `""` | ✅ `boomtime` | `:416` |
| `BOOM_OIDC_CLIENT_SECRET` | Client secret | `""` | ✅ via ExternalSecret | `:417` |
| `BOOM_OIDC_REDIRECT_URL` | Callback | `""` | ✅ `…/auth/callback/oidc` | `:418` |
| `BOOM_OIDC_AUTHORIZE_URL` | Override when discovery can't be reached | `""` | — | `:415` |
| `BOOM_AUTHENTIK_GROUP_TO_ROLE` | Maps IdP groups → boomtime roles | `""` | ✅ `boomtime-admin:admin,boomtime-full:full,boomtime-light:light` | `:419` |
| `BOOM_OIDC_AUTOPROVISION` | Create a local account on first OIDC login | `false` | ✅ `false` | `:420` |
| `BOOM_OIDC_AUTOLINK_EMAIL` | Auto-link an OIDC identity to an existing account by matching email | `false` | ✅ `false` | `:421` |

---

## 🔌 Integrations

| Flag | What it gates | Default | Prod | Read at |
|---|---|---|---|---|
| `BOOM_HARDCOVER_DRYRUN` | **Defaults to ON — Hardcover writes are blocked and logged, not sent.** Set `false` to actually push finished books | `true` | — **DRY-RUN** | `:428` |
| `BOOM_AUDIBLE_SYNC_INTERVAL` | Period of the `audiobooks-audible-sync` job | `6h` | — `6h` | `:521` |
| `BOOM_GITHUB_STATS_REFRESH_INTERVAL` | Period of the `github-stats-refresh` job | `8h` | — `8h` | `:520` |
| `BOOM_GITHUB_OAUTH_CLIENT_ID` / `_SECRET` / `_REDIRECT_URL` | GitHub OAuth App. **All three plus `OAUTH_STATE_SIGNING_KEY` must be present** or `github_connect_enabled` stays false even with the feature flag on | `""` | ✅ (secret + redirect URL) | `:429`–`:431` |
| `BOOM_OAUTH_STATE_SIGNING_KEY` | Signs the OAuth state param | `""` | ✅ via ExternalSecret | `:432` |
| `GITHUB_TOKEN` | Server-wide GitHub token fallback. **No `BOOM_` prefix** | `""` | — | `:452` |
| `WAKATIME_API_KEY` | Server-wide wakatime.com key the importer falls back to. **No `BOOM_` prefix** | `""` | — | `:492` |
| `BOOM_LLM_API_KEY` / `_BASE_URL` / `_MODEL` | LLM used for avatar prompt synthesis + label prompts | `""` / `https://api.openai.com/v1` / `gpt-4o-mini` | ✅ ollama shim, `Cydonia-24B` | `:464`–`:466` |
| `BOOM_COMFYUI_SHIM_URL` / `_MODEL` | Image backend for label artwork + avatars | `""` / `sdxl-illustrious-xl` | ✅ `http://comfyui-shim:8012`, `chroma-hd` | `:458`–`:459` |
| `BOOM_LABEL_IMAGES_RECONCILE` | Catalog reconcile mode | `auto` | — `auto` | `:460` |
| `BOOM_LABEL_IMAGE_CONCURRENCY` | Parallel image generations. Clamped to 1–16; junk reverts to default | `2` | — `2` | `cmd/boomtime/main.go:980` |
| `BOOM_S3_ENDPOINT` / `_BUCKET` / `_ACCESS_KEY` / `_SECRET_KEY` / `_USE_SSL` | Durable S3 cache for social-card renders. **All four of endpoint/bucket/access/secret required** (`S3Enabled()`); until then cards render live | `""` | ✅ MinIO `boomtime-cards` | `:512`–`:516` |
| `BOOM_REMOTE_WRITE_URL` / `_TOKEN` | Optional heartbeat mirroring to another instance | `""` | — | `:485`–`:486` |

---

## ⚙️ Runtime topology — role, broker, jobs

boomtime runs as **three** processes off one image. This group is what distinguishes them.

| Flag | What it gates | Default | Prod | Read at |
|---|---|---|---|---|
| `BOOM_ROLE` | `all` · `server` · `worker`. Also settable as `--role=` | `all` | ✅ server: `--role=server` arg · worker + ScaledJob: `worker` | `:502` |
| `BOOM_QUEUE_BROKER` | `inprocess` or `rabbitmq` for the **image-job** pipeline. This is the worker-topology cutover switch | `inprocess` | ✅ `rabbitmq` · **ScaledJob overrides to `inprocess`** | `:503` |
| `BOOM_JOBS_PROVIDER` | Transport for **catalyst-go-jobs**: `local` (Postgres is the broker) or `rabbitmq`/`celery`. Independent of `QUEUE_BROKER` above | `local` | — `local` | `:519` |
| `BOOM_JOBS_UNIFIED` | Folds the bespoke imagejobs producer in as the `label-image` job kind | `false` | ✅ `true` | `:522` |
| `BOOM_JOBS_DRAIN` | One-shot mode: drain every due job, then **exit**. This is what makes a KEDA ScaledJob pod work | `false` | ✅ `true` **on the ScaledJob only** | `:523` |
| `BOOM_JOBS_KINDS` | CSV allowlist — claim *only* these kinds | `""` (any) | ✅ ScaledJob: `avatar-render,label-image` | `:524` |
| `BOOM_JOBS_EXCLUDE_KINDS` | CSV denylist — never claim these. Routes heavy kinds away from the always-on server | `""` | ✅ `avatar-render,label-image` · **ScaledJob clears it to `""`** | `:525` |
| `BOOM_RABBITMQ_URL` / `_QUEUE` / `_MGMT_URL` | AMQP connection, queue name, mgmt UI link | `""` / `boomtime.image-jobs` / `""` | ✅ all three | `:504`–`:508` |
| `BOOM_REDIS_ADDR` / `_PASSWORD` | Dragonfly, for the cross-pod worker-log relay | `""` | ✅ `boomtime-cache…:6379` | `:506`–`:507` |

> ⚠️ The ScaledJob deliberately sets `BOOM_JOBS_EXCLUDE_KINDS=""` — env beats `envFrom`, and
> without the clear it would both *include* and *exclude* `avatar-render` and claim nothing.

---

## 🖥️ Frontend gates — URL params & browser storage

No build step or redeploy needed; these live in the browser.

### The one that matters

**`?enable_beta_user_registration=true`** — append to **any** path to force the beta
onboarding flow (`welcome → why → demo → signup`). `BetaOnboardingGate` in
`web/src/app/App.tsx:163` sees every route and redirects to `/onboarding`. Works **while
logged in**, by design — you can walk the new signup UX without logging out.

- **Carrier:** sessionStorage `boomtime:beta:user_registration` — survives navigation in the
  tab, clears when the tab closes.
- **Exit:** the "Exit preview" link in the banner, or `?enable_beta_user_registration=false`.
- **Server veto:** `beta_flags.user_registration` from `GET /api/v1/config/public`. Only an
  explicit `false` blocks it; unknown/loading defaults to allowed.
- **Read at:** `web/src/features/onboarding/betaRegistration.ts:21`

### Storage keys

| Key | Store | What it gates | Reset |
|---|---|---|---|
| `boomtime-welcomed` | local | First-run welcome modal on `/app`. Set to `1` on dismiss | `localStorage.removeItem('boomtime-welcomed')`, or incognito |
| `boomtime:beta:user_registration` | session | The beta onboarding preview (above) | Close the tab, or `=false` |
| `boomtime.catalog.source` | local | Widget catalog `mine` ↔ `sample` toggle | — |
| `boomtime-timerange` | local | Last-used dashboard time range | — |
| `boomtime-sidebar-collapsed` | local | Collapsed left rail | — |
| `boomtime.reading.range` | local | Reading-stats range | — |
| `boomtime.widgets-panel.width` | local | Widgets panel width | — |
| `boomtime.amazon.session` | session | In-flight Connect-Amazon handshake | — |
| `boomtime:chunk-reload-at` | session | Guard against a chunk-reload loop after a deploy | — |
| `catalyst-ui-annotations` / `-autosync` | local | Devtools annotation subsystem. **localStorage-only — boomtime has no sync endpoint** | — |

### Build-time

| Flag | What it gates | Notes |
|---|---|---|
| `VITE_GA_MEASUREMENT_ID` | Google Analytics page-view reporting | Baked in at `yarn build`; absent = no analytics |
| `import.meta.env.DEV` | Dev-console logging in the annotation subsystem | **Not** the devtools visibility gate — that is admin-only via `useIsAdmin()` |

> The `/catalog` public widget gallery has **no flag at all** — it's unauthed, renders 40
> widgets on seeded sample data, and is always on. See [DEMO.md](DEMO.md).

---

## 🩺 Diagnostics & tuning

Not features — knobs. Safe to ignore unless you're debugging.

| Flag | What it does | Default | Prod |
|---|---|---|---|
| `BOOM_ENV` | `dev` \| `prod`. Drives log format (text vs **JSON**), cookie security, and the DB-tracer defaults | `prod` | ✅ `prod` |
| `BOOM_LOG_LEVEL` | stdout threshold. The Logs tab always sees DEBUG regardless | `info` | ✅ `info` |
| `BOOM_HTTP_LOG` | The `msg="http request"` access log — **every ingest/API dashboard is built on this** | `true` | ✅ `true` |
| `BOOM_DB_LOG_QUERIES` | Query tracer | `true` in dev | — off |
| `BOOM_DB_LOG_ARGS` | Include bound args (**leaks values**) | `false` | — off |
| `BOOM_DB_N1_THRESHOLD` / `_DUP_THRESHOLD` | N+1 detector sensitivity | `20` / `10` | — |
| `BOOM_DB_EXPLAIN_SLOW_MS` | Auto-EXPLAIN slow queries; `0` disables | `0` prod, `250` dev | — off |
| `BOOM_STATS_CACHE_TTL` | Stats cache, seconds; `0` disables | `30` | ✅ `30` |
| `BOOM_STATS_WORK_MEM` | Postgres `work_mem` for aggregations. Must match `^[0-9]+(kB\|MB\|GB)$` or it's ignored | `256MB` | ✅ `256MB` |
| `BOOM_OTHER_MAX_SHARE` | Max share of a donut collapsed into "Other". Must be `0 < v ≤ 1`; junk reverts | `0.25` | — |
| `BOOM_RESTORE_MAX_BYTES` | Upload cap on a restore archive | 4 GiB | — |
| `BOOM_DEFAULT_TIMEZONE` | Fallback tz for users who haven't set one | `""` | ✅ `America/Los_Angeles` |
| `BOOM_DISABLE_RATE_LIMIT=1` | **Testing hook** — disables every limiter | off | — |
| `BOOM_RATELIMIT_<GROUP>_RATE` / `_BURST` | Per-group limiter override. Groups: `AUTH_WRITE` (10/min per IP), `WAKATIME_PROBE` (5/min per user), `DEFAULT` (60/s per IP). Malformed values drop back to the default **and log a WARN** | per-group | — |
| `BOOM_GRADE_*` (12 knobs) | Grade-ring medians and weights, plus `_MIN_RANGE_DAYS` | see `:344` | — |
| `BOOM_PORT` · `BOOM_API_PREFIX` · `BOOM_BADGE_URL` · `BOOM_DASHBOARD_PATH` · `BOOM_SHIELDS_IO_URL` | Serving/URL basics | `8080` · `""` · `""` · `""` · shields.io | ✅ port, badge URL |
| `BOOM_DB_HOST` / `_PORT` / `_NAME` / `_USER` / `_PASS` | Postgres connection | `localhost:5432/boomtime` | ✅ CNPG + Secret |
| `BOOM_TEST_DATABASE_URL` · `BOOM_REQUIRE_DB=1` | Test harness only — `REQUIRE_DB` turns a skipped DB test into a failure | — | n/a |

---

## 📍 Where prod values actually come from

The overlay assembles `boomtime-config` from a base ConfigMap plus one strategic-merge
patch per feature, so each flag lands next to the comment explaining it:

```
k8s/base/configmap.yaml                     BOOM_ENV, BOOM_ENABLE_REGISTRATION, …
k8s/overlays/talos00-knowledgedump/
  app-config.yaml          BOOM_FEATURE_GITHUB_STATS · _USER_MODEL · _ADMIN_CLI · _BOOKS
  oidc-config.yaml         BOOM_AUTH_PROVIDER + every BOOM_OIDC_*
  queue-config.yaml        BOOM_QUEUE_BROKER=rabbitmq  ← the cutover switch
  jobs-config.yaml         BOOM_JOBS_EXCLUDE_KINDS
  label-images-config.yaml BOOM_FEATURE_LABEL_IMAGES + ComfyUI
  llm-config.yaml          BOOM_LLM_*
  s3-config.yaml           BOOM_S3_* (non-secret)
  books-config.yaml        BOOM_FEATURE_BOOKS
  tz-config.yaml           BOOM_DEFAULT_TIMEZONE
  keda-scaledjob-jobs.yaml BOOM_JOBS_DRAIN/_KINDS/_EXCLUDE_KINDS  (env, beats envFrom)
```

Secrets (`BOOM_OIDC_CLIENT_SECRET`, `BOOM_GITHUB_OAUTH_*`, `BOOM_ENCRYPTION_KEY`,
`BOOM_S3_*_KEY`, DB creds) arrive as ExternalSecrets / CNPG secrets, never as ConfigMap keys.

**To read the effective set** without guessing which patch won:

```bash
kustomize build k8s/overlays/talos00-knowledgedump \
  | yq 'select(.kind=="ConfigMap" and .metadata.name=="boomtime-config").data'
```

**To see what the running server advertises** (unauthenticated, safe to curl):

```bash
curl -s https://boomtime.knowledgedump.space/api/v1/config/public | jq
```

---

← back to the [README](README.md) · the visual tour: [DEMO.md](DEMO.md) · full env reference: [.env.example](.env.example)
