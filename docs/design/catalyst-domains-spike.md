# Architecture Spike: Boomtime as a Data-Fusion Core with Pluggable Domains

Status: SPIKE (explore + recommend) · Date: 2026-08-12
First tenant: **catalyst-books** (v1) · First lift-out (later): **catalyst-waka**

---

## 1. Vision & goal

Boomtime becomes a **fusion + analytics core** that owns identity, the job substrate, the widget/dashboard engine, the response cache, and a cross-domain metric model — and nothing domain-specific. Each **domain package** owns exactly one data domain end-to-end: its ingest (push + pull jobs), its tables/DTOs, its per-user secrets, the metrics/series it publishes, and the widgets + FE surface it contributes. Domains plug into the core through a small set of registration seams instead of being hand-wired into god-files. The payoff is *fusion*: correlating coding-time against reading-time against an HR signal on one calendar, which today is faked by a single hardcoded field (`StatsPayload.GithubDailyTotal`).

The plan is deliberately **incremental and non-big-bang**. We stand up the domain skeleton + contract NOW and land **catalyst-books** as the first tenant *without moving any existing wakatime code*. The current wakatime analytics stack keeps running untouched in `internal/*`. Only after books proves the contract do we lift wakatime into `catalyst-waka`, and only after that do we generalize the fusion/analytics layer off the `heartbeats` god-table. Every phase is independently shippable behind a feature flag; `main` stays deployable the whole way.

---

## 2. Current architecture reality (honest map)

Boomtime is a Go monolith (`github.com/TheBranchDriftCatalyst/boomtime`) that is **already a multi-domain host in embryo** — but every domain except the first reimplements storage + rollup + aggregation from scratch, and the analytics/widget/schema core is physically bolted to wakatime shapes.

**What is genuinely core-grade and reusable today:**
- `internal/jobs` (catalyst-go-jobs): `Registry.Register(kind, Handler)`, `Provider` (Local Postgres `FOR UPDATE SKIP LOCKED` | AMQP), leader-singleton `Scheduler`, `Store`, `Notifier`. Doc-declared free of boomtime imports → **lift-ready**. This is the one clean plug point.
- Identity substrate: `users` + `auth_tokens`/`refresh_tokens`/`oidc_sessions`/`user_external_identities`/`user_avatars`; `internal/auth`, `internal/identity`, `internal/oauth`, `internal/cardstore`. Every domain FKs `users(username)`.
- `internal/widget/specs.json` — ONE `//go:embed`'d file also imported by the FE via `@widget-specs` → `specs.ts`, drift-guarded by `spec_test.go` + `specs.test.ts`. The single strongest fusion asset.
- Cross-cutting plumbing: `internal/config` (BOOM_FEATURE_* gates), `internal/cache` (per-owner TTL blob cache, fully domain-neutral), `apihelpers`, `apierr`, `logging`, `openapi`, `meta`. `internal/auth/crypto.go` (AES-256-GCM under `BOOM_ENCRYPTION_KEY`).

**How coupled the core is to wakatime/coding — this is what decides difficulty:**
- **`internal/db/axes.go` is the linchpin.** A hardcoded registry of 8 coding axes (project/language/editor/plugin/machine/platform/branch/category) tied to columns on `heartbeats`/`hb_rollup_daily`. `curation`, `spaces` (rules), `awards`, `labels`, and all of `stats` derive their hide/scope/predicate SQL from this one list. New domains cannot register their own axes.
- **`internal/stats` is one 39-file package serving two domains through one router.** `routes.go` registers coding endpoints (`/stats/loc`, `/punchcard`, `/sessions`, `/momentum`, `/projects`, `/leaderboards`, `/files`) *and* health endpoints (`/stats/health`, `/workouts`) side by side. "Analytics core" and "coding analytics" are the same package.
- **`heartbeats` is a wakatime-shaped god-table** (editor/plugin/branch/language/cursorpos/lineno/is_write/ai_* — plus `workout_*` columns bolted on). "Time spent" is derived from `gap_seconds` between heartbeats; grade/streaks/momentum/sessions/punchcard all assume that metric. Reading-pages and workout-minutes don't map onto it. Workouts already had to write *through* heartbeats as `ty='workout'` to reuse aggregation.
- **`widget.Data` + binding resolvers are a hard chokepoint.** `Data = {StatsPayload, Grade, Punchcard, Momentum, Sessions, Goals, Identity}`; `resolveResources`/`resolveSeries` bind hardcoded coding strings ("languages", "daily-total"). Any backend-rendered (`target:"both"`) widget for a new domain forces edits to the core widget package + drift-guard fixtures. Non-coding domains are pushed to `target:"fe-only"` self-fetch (which is exactly what github-* and wellness widgets already do), losing the SVG-embed/OG-card surface.
- **`internal/handler` and `internal/server` are god-composition files.** Each imports ~10–12 domain packages (admin, curation, goals, ingest, identity, importer, spaces, stats, widgets, awards, meta). No registration seam — every domain is hand-edited into both.
- **Enumeration seams hardcode each domain by name:** `cmd/boomtime/rotate.go` re-encrypts each encrypted column by name (wakatime + github); `internal/db/dump.go` enumerates backup tables/columns by name. A new domain's secret is *silently stranded on next rotation* and its table *silently excluded from backups* if not added — the exact incident class CLAUDE.md's encryption section warns about.
- **Three parallel job systems already coexist:** importer's own `import_jobs`/`import_job_logs` + in-process `Hub` (wakatime backfill), `queue/imagejobs` (RabbitMQ label images), and catalyst-go-jobs. A books domain must resist becoming a fourth.

**The two existing "second domains" prove both the feasibility and the failure mode:**
- `internal/github` — cleanest precedent: own `github_stats_cache` (one-row-per-user JSONB blobs, no migration to add a metric), own `github-stats-refresh` job kind on the Scheduler, own `encrypted_github_token`, fe-only self-fetching widgets. **This is the template catalyst-books copies.**
- Health/Workout — `health_samples` is a genuinely generic `owner/kind/unit/qty/ts/meta-jsonb` **fact table** + `health_rollup_daily`. Best structural template for books rows — but it's half-extracted and interleaved (tables in baseline `00001`, ingest in the shared `internal/ingest`, `/stats/health` in the shared stats package), and it cheats by writing workouts through heartbeats.

**Verdict:** Additive schema (numbered goose migrations) and data-layer CRUD are cleanly additive. Job wiring, routes, feature flags, auth caps, `rotate.go`, and `dump.go` all still require core edits. "Add a domain without touching core" is ~half true today; the missing piece is a `DomainModule` seam. Standing up catalyst-books as a self-contained `internal/github`-shaped package is **low-risk and needs zero refactoring of the coupled analytics core** — which is precisely why it's the right v1.

---

## 3. What a "domain package" is, concretely

A domain is a Go package implementing a `DomainModule` contract, plus a matching FE feature folder. The contract is a bundle of the seams that **already exist** — we're formalizing them, not inventing them.

```go
// internal/core/domain/module.go  (new, tiny)
type Module interface {
    Name() string                 // "books", "waka", "github", "health"
    FeatureFlag() string          // "BOOM_FEATURE_BOOKS"; Enabled() gates everything below
    Migrations() fs.FS            // embedded goose migrations owned by this domain
    RegisterRoutes(e *echo.Group, deps Deps)   // push/bulk ingest + query endpoints
    RegisterJobs(reg *jobs.Registry, sched *jobs.Scheduler, deps Deps) // kinds + intervals
    EncryptedColumns() []secret.Column  // {table, column, statusColumn} for rotate + backup
    BackupTables() []string             // tables to include in whole-DB export
    Capabilities() []auth.Cap           // e.g. CapIngestBooks
    Metrics() []metric.Series           // daily series this domain publishes to fusion (P2+)
    WidgetSpecs() fs.FS                 // this domain's slice of the widget catalog (P2+)
}
```

Concretely, a domain implements each seam against today's real code:

| Seam | Exists today as | Domain provides |
|---|---|---|
| **Schema** | numbered goose migration over embedded FS (`00057_*.sql`); additive, touches no existing file | its own `Migrations() fs.FS` |
| **Data CRUD** | `internal/db/github_token.go`, `github_stats_cache` accessors | its own DAL functions (own package) |
| **Per-user secret** | `internal/db/wakatime_key.go` / `github_token.go` (Set/Update/Get/GetInfo/List/Rotate/Clear, AES-GCM via `auth.Encrypt/Decrypt`) | declares `EncryptedColumns()`; core's registry drives rotate + backup automatically |
| **Pull ingest job** | `internal/github`: `const GithubStatsRefreshKind`, `Service.SyncUser`, `jobReg.Register(kind, HandlerFunc{fan over ListUsersWith…Token → SyncUser})`, `sched.Register(kind, interval)` | `RegisterJobs()` |
| **Push/bulk ingest** | `internal/ingest/routes.go` `Register(e, h)` pattern (`heartbeats.bulk`, `workouts.bulk`), `InvalidateOwnerCache` on write | `RegisterRoutes()` |
| **Capability gate** | `auth.CapIngestHeartbeats` | `Capabilities()` → e.g. `CapIngestBooks` |
| **Feature flag** | `config.FeatureGithubStats` + `Enabled()` helper | `FeatureFlag()` |
| **Widget** | `specs.json` entry, `target:"fe-only"` via `OverviewWidgetRenderer` self-fetch | `WidgetSpecs()` + FE catalog entry |
| **FE feature folder** | `web/src/features/widgets`, `web/src/features/import`, github/wellness react-query contexts | `web/src/features/books/` + nav + settings |

The core keeps a `domain.Registry` (a slice of `Module`s wired once in `cmd/boomtime/main.go`). Its **payoff:** `rotate.go` iterates `registry.EncryptedColumns()` instead of hardcoding, `dump.go` iterates `registry.BackupTables()`, and `internal/server`/`internal/handler` iterate `registry.RegisterRoutes()` instead of importing each domain. That single indirection is what turns "edit five god-files per domain" into "append one `Module` to the registry."

---

## 4. Proposed directory structure (before → after)

We do **not** move existing wakatime code in P0. We introduce `internal/core/` (thin, mostly re-exports/aliases at first) and `internal/domains/` (new, holds only books initially). Existing packages keep their paths until the P1 lift.

### Go — before

```
internal/
  jobs/ jobsevents/          # generic queue  (→ core)
  auth/ oauth/ identity/ cardstore/   # identity  (→ core)
  config/ cache/ apihelpers/ apierr/ logging/ openapi/ meta/   # plumbing (→ core)
  widget/ widgets/ goals/    # widget+goal engine (→ core, decouple later)
  stats/                     # 39 files: coding + health analytics (→ split)
  db/ model/                 # 90+ files, all domains co-mingled (→ split)
  ingest/                    # heartbeats(waka) + workouts/health_samples
  wakatime/ importer/ curation/ spaces/ awards/ labels/ labelcatalog/  # waka
  github/                    # cleanest existing domain
  comfyui/ queue/imagejobs/ worker/labelimages/   # media/image render
  server/ handler/ admin/    # god-wiring + admin console
cmd/boomtime/                # main.go job block, rotate.go
```

### Go — after (target; reached incrementally)

```
internal/
  core/
    domain/          # NEW: Module interface, Registry, Deps
    jobs/  jobsevents/
    identity/ auth/ oauth/ cardstore/
    config/ cache/ apihelpers/ apierr/ logging/ openapi/ meta/
    widget/ widgets/ goals/          # engine; binding vocab generalized (P2)
    stats/                           # domain-NEUTRAL aggregation: scope-splicer,
                                     # axis-registry mechanism, rollup, TTL, top-N
    metric/                          # NEW (P2): generic per-day fact/series registry
    server/ handler/                 # iterate domain.Registry, no domain imports
  domains/
    books/                           # v1, FIRST TENANT (P0)
      model/     # Book, BookSource, ReadingState DTOs
      db/        # books/book_sources/reading_state accessors + migrations FS
      ingest/    # Audible + Kindle scrobble jobs (cloned from internal/github)
      amazon/    # adp_token device reg + RSA signing (mkb79 port)
      hardcover/ # push sync job
      jobs.go    # BooksAudibleSyncKind, BooksHardcoverPushKind
      routes.go  # RegisterRoutes: books.bulk, /books query
      module.go  # implements core/domain.Module
    waka/          # P1 lift target: wakatime importer curation spaces awards
                   # labels labelcatalog + coding stats + heartbeats DAL/DTOs
    github/        # lift internal/github here (P1, easy)
    health/        # lift health_samples/workouts here (P1)
  admin/           # thin shell composing per-domain admin fragments
```

Package-layout recommendation for a domain (books shown): `internal/domains/books/{model,db,ingest,amazon,hardcover}` + top-level `jobs.go`, `routes.go`, `module.go`. Migrations live at `internal/domains/books/db/migrations/*.sql` embedded via `//go:embed` and surfaced through `Module.Migrations()`. **One Go module, one Postgres DB** (see §8) — domains are packages, not separate modules, in v1.

### web/src/features — before → after

```
before: features/{widgets, import, github, wellness, overview, ...}
after:  features/
          core/      # SpecRenderer, catalog, dashboard grid, @widget-specs
          books/     # NEW: connect-amazon, reading list, react-query context,
                     # settings panel, catalog entries (dashboardScopes), nav item
          waka/      # (P1) coding dashboard moved here
          github/ health/   # (P1) existing self-fetch domains normalized
```

---

## 5. The fusion problem — generalizing analytics for N domains

Today there is **no generic fact/metric model**. "Time spent" (`gap_seconds`) is the universal metric, and the only cross-domain correlation that exists is one hardcoded overlay: `StatsPayload.GithubDailyTotal`, a per-day GitHub contribution series aligned to the coding calendar's `DailyTotal`. That proves the appetite but is O(domains²) hardcoded fields if repeated — it will not scale to coding × reading × HR.

**Recommendation: domains contribute *pre-aggregated daily series*; the core fuses/charts them.** Do NOT try to make every domain emit raw "events" into one generic `heartbeats`-style fact table and reuse the wakatime gap/rollup engine — reading-pages and workout-minutes have no `gap_seconds` semantics, and forcing them in is exactly the `ty='workout'` mistake. Instead:

```go
// internal/core/metric  (P2)
type Series struct {
    Domain string   // "waka" | "books" | "health"
    Key    string   // "coding-seconds" | "pages-read" | "resting-hr"
    Unit   string   // "seconds" | "pages" | "bpm"
    Agg    Agg      // sum | avg | max
}
// Each domain implements: DailySeries(owner, key, t0, t1) ([]DayPoint, error)
```

The core owns a **fusion layer** that requests any registered `Series` from any domain over a shared date window and overlays/correlates arbitrary pairs — replacing the single `GithubDailyTotal` field with a general `overlays: [{domain,key}]` request. This reuses what's already generic and good: the **per-owner TTL cache** (`internal/cache`, key already `owner|name|t0|t1|limit|space`), the **`capWithOther`/top-N + weekly-bucket payload bounding**, and the **curation/scope predicate-splicer** (`applyScopes`/`injectAfter`/`regroupStatRows` — a genuinely reusable hide/rename/scope mechanism) *once `axes.go` becomes a per-domain axis registry instead of a hardcoded coding list*.

The wakatime-specific derivation (gap→duration, `hb_rollup_daily`, `GetUserActivity`, grade/streaks/momentum/sessions/punchcard) moves wholesale into `catalyst-waka` and simply *implements* `DailySeries("waka","coding-seconds",…)`. GitHub and Health already have their own rollups (`refreshHealthRollup`, `github_stats_cache`) — they each expose a `Series` and stop being special-cased. **Books does the same from day one:** its rollup is `pages-read`/`minutes-listened` per day, and it participates in fusion via `DailySeries` the moment the metric registry exists — no heartbeat analogue needed.

For widgets: keep `specs.json` as the single source of truth (its cross-language drift guard is the crown jewel). Generalize the **binding vocabulary** from hardcoded coding strings to a namespace (`waka:languages`, `books:pages-daily`, `health:resting-hr`) resolved through a per-domain data-provider interface, replacing the fixed `widget.Data` struct + `resolveResources`/`resolveSeries` switches. Until that lands (P2), books ships `target:"fe-only"` self-fetch widgets exactly like github/wellness — full feature parity with the other real domains, zero core edits.

---

## 6. catalyst-books v1, end-to-end (proving the contract)

Books is BOOMTIME-FIRST: boomtime is source of truth, scrobbles Audible + Kindle, pushes to Hardcover. It slots in as an `internal/github`-shaped domain and touches **zero** wakatime/analytics code.

**1. Connect Amazon (per-user secret).** Audible + Kindle share ONE Amazon device registration (`adp_token` + RSA signing, ports of `mkb79/audible` + `mkb79/kindle`). A single "Connect Amazon" FE action stores the device credential:
- Migration `internal/domains/books/db/migrations/00001_books.sql`: `ALTER users ADD encrypted_amazon_device bytea, amazon_device_status text, amazon_device_checked_at timestamptz` — modeled on `00048_github_token.sql`.
- `books/db/amazon_device.go` — copy `internal/db/github_token.go` shape (Set/GetInfo/Get/List/Rotate/Clear), encrypt via `internal/core/auth.Encrypt`. Declared in `Module.EncryptedColumns()` so `rotate.go` and `dump.go` pick it up **automatically** — closing the "silently stranded on rotation / dropped from backup" gap by construction.

**2. Ingest jobs (pull).** Cloned from `internal/github/sync.go`+`jobs.go`:
- `const BooksAudibleSyncKind = "books-audible-sync"`, `BooksKindleSyncKind`, `BooksHardcoverPushKind`.
- `books.Service.SyncUser(owner)` — idempotent: pull Audible library + Kindle reading positions via the Amazon device creds, upsert into `books`/`book_sources`/`reading_state`.
- In `cmd/boomtime/main.go` (or, post-P0, auto via `Module.RegisterJobs`): `jobReg.Register(BooksAudibleSyncKind, HandlerFunc{fan over ListUsersWithAmazonDevice → SyncUser})`, `sched.Register(kind, cfg.BooksRefreshInterval)`, all gated on `config.FeatureBooks`.
- Backfill sources ride catalyst-go-jobs (NOT a cloned importer Hub): Goodreads CSV/RSS → dates+ratings; Amazon privacy export → start-dates/time-read.

**3. Data model** (`internal/domains/books/model` + `db`), migration `00001_books.sql`, all `owner text REFERENCES users(username) ON DELETE CASCADE`:
- `books` — canonical work + `hardcover_id`, match info.
- `book_sources` — one row per external identity (audible/kindle/goodreads ASIN/ISBN).
- `reading_state` — `status` (want/reading/read/paused/dnf), `percent`, `started_at`, `finished_at`, `rating`, `read_count`.
Follow `github_stats_cache`: keep flexible fields in JSONB so new attributes need no migration.

**4. Hardcover sync (push).** `BooksHardcoverPushKind` catalyst-go-jobs periodic job: diffs local `reading_state` against Hardcover and pushes updates. Same `Service.SyncUser`-idempotent shape.

**5. Books dashboard widget.** Declare `books-reading` / `books-pages-daily` in `specs.json` as `target:"fe-only"`. FE: `web/src/features/books/` with a react-query context (mirror `OverviewDataContext`/`GithubStatsPayload`), a `catalog.ts` entry with `dashboardScopes` so it lands on the grid via `OverviewWidgetRenderer`, a "Connect Amazon" settings panel, and a nav item. When the P2 metric registry lands, books implements `DailySeries("books","pages-read",…)` and its calendar can be overlaid against `waka:coding-seconds` — the first *real* fusion.

**6. Feature gate.** `config.FeatureBooks` (`BOOM_FEATURE_BOOKS`) + `BooksEnabled()` helper folding in interval/dependency checks; the whole surface is dark until flipped. `main` ships with it off.

This is the full domain contract exercised against real seams (`jobs.Registry.Register`, encrypted-secret CRUD, additive migration, `Register(e,h)`, `CapIngestBooks`, feature gate, FE feature folder) with **no analytics-core generalization required**.

---

## 7. Incremental migration path (each phase independently shippable, `main` stays deployable)

**P0 — Domain skeleton + catalyst-books as first tenant. Move zero existing code.**
- Add `internal/core/domain` (`Module`, `Registry`, `Deps`) and a `domain.Registry` wired in `main.go`. Existing wakatime packages stay exactly where they are.
- Refactor the two enumeration seams to consult the registry: `rotate.go` → `registry.EncryptedColumns()`, `dump.go` → `registry.BackupTables()`. Register wakatime + github columns/tables through thin `Module` stubs so behavior is identical (pure refactor, guarded by existing tests). This alone kills the "stranded secret / dropped backup" class.
- Build `internal/domains/books` implementing `Module`; register it. Ship behind `BOOM_FEATURE_BOOKS=off`.
- FE `features/books/` behind the same gate.
- *Shippable:* books works when flagged on; wakatime untouched; `main` deployable.

**P1a — Lift the two clean domains (`internal/github`, health) into `internal/domains/`.**
- Move `internal/github` → `internal/domains/github` implementing `Module`; it barely changes (already self-contained). Second validation of the contract.
- Untangle health: stop workouts writing through `heartbeats`; move `health_samples`/`workout_details` DAL + ingest + `/stats/health` into `internal/domains/health`. This is the entangled-seam pilot — do it before wakatime because it's smaller but touches db+model+ingest+stats simultaneously, so it de-risks the big lift.
- *Shippable per domain:* each is a self-contained PR behind its flag.

**P1b — Generalize `axes.go` into a per-domain axis registry.** Prerequisite for lifting curation/awards/labels/spaces/stats. Convert the hardcoded coding list into a registration API; wakatime registers its 8 axes; the scope/hide/rename splicer now works on any domain's table. Highest-leverage, highest-risk single refactor — do it as its own phase with the dual test suite green.

**P2 — Lift wakatime into `catalyst-waka`.** Move `wakatime`, `importer` (+ its `import_jobs` — consider migrating onto catalyst-go-jobs to kill a parallel job system), `curation`, `spaces` rules, `awards`+`labels`+`labelcatalog`, and the coding-specific half of `stats` (loc/projects/punchcard/sessions/momentum/leaderboards/grade/streaks) into `internal/domains/waka`. Split `internal/stats` into a domain-neutral aggregation engine (`core/stats`: splicer, rollup mechanism, TTL, top-N) vs waka stat endpoints. Split `internal/model` and `internal/db` per-domain. Largest surgical effort; do it *after* books+github+health have proven the seams so you're moving code into a known-good shape. Remove `config→stats` GradeConfig coupling (move `GradeConfig` into waka, inject via interface).

**P3 — Generalize the fusion layer.** Add `internal/core/metric` (`Series`/`DailySeries`), replace `StatsPayload.GithubDailyTotal` with a general overlay request, generalize the widget binding vocabulary (`waka:*`/`books:*`) off the fixed `widget.Data` struct. Now books/health/coding correlate on one calendar. Re-scope the drift-guard tests (`spec_test.go`, `TestKindsMatchFrontendCatalog`) for multi-domain catalogs.

**P4 — Replace god-wiring.** `internal/server`/`internal/handler` iterate `registry.RegisterRoutes()`; `admin` becomes a thin shell composing per-domain admin fragments. Adding a domain now touches zero core files.

Ordering rationale: P0 delivers books value + fixes two real incident classes with almost no risk. P1a/P1b/P2 are strictly "make the split real" and each is independently revertible. Fusion (P3) is deferred until there are ≥2 domains actually worth correlating — avoiding premature abstraction.

---

## 8. Risks, tradeoffs, and open questions for DJ

1. **One Go module vs multiple / monorepo-in-boomtime vs separate `catalyst-*` repos.** The `catalyst-` naming implies separate repos, but v1 domains share `users(username)` FKs, the widget `specs.json`, and `internal/auth/crypto.go` — separate modules/repos means versioned internal APIs and a painful `replace` dance immediately. **Recommendation: one module, `internal/domains/*` packages, for v1.** Revisit only if a domain genuinely needs independent release cadence. Decide now so books lands in the right place.

2. **Shared DB vs per-domain schemas.** Today: one linear goose sequence, one Postgres DB, one 24k-LOC `internal/db`. Options: (a) keep one DB, per-domain migration FS + `search_path=public` (simplest, keeps cross-domain fusion joins trivial); (b) per-domain Postgres schema namespace; (c) per-domain DB. **Recommendation: (a).** But confirm you're OK with domains sharing one migration *ordering* namespace (numbering collisions across parallel domain work — books `00001` vs a global counter). How do we allocate migration numbers across domains?

3. **How much to generalize the analytics engine now vs later.** The coupling (`axes.go`, `stats`, `widget.Data`, `heartbeats`) is deep. Generalizing it in P0 would stall books for months. The spike recommends **defer to P1b–P3** and ship books `fe-only`. Are you comfortable with books being fe-only self-fetch (no backend SVG/OG-card render, no curation/scope engine) until the metric registry lands? That's the main v1 feature gap.

4. **The fusion model itself: pre-aggregated `Series` vs a generic raw fact table.** I recommend domains publish daily `Series` and keep their own storage/rollup, rather than a universal event table + shared gap/rollup engine (which mis-fits non-time-spent domains). Confirm you agree — the alternative (one generic fact table) is more "pure" but risks re-creating the `heartbeats` god-table problem for all domains.

5. **Three parallel job systems → don't make books a fourth.** Books backfill (Goodreads/Amazon export) is multi-step and could tempt a clone of importer's durable `Hub`/`import_jobs`. **Recommendation: books rides catalyst-go-jobs only.** Do you want P0 to also migrate the wakatime importer off `import_jobs` onto the generic queue, or leave that for P2? (It's the single biggest "standardize before adding" lever.)

6. **`comfyui`/`queue/imagejobs`/`worker/labelimages` classification.** Generic async image-render infra, but its only consumer today is waka award/label badges. Shared-infra or waka-domain? Picking shared-infra now is premature abstraction with one consumer. Does books/media plausibly reuse image-gen (cover art?) — if yes, promote to core; if not, let it ride to `catalyst-waka` until a second consumer appears.

7. **`DomainModule` invasiveness vs payoff timing.** The registry only pays off once `rotate.go`/`dump.go`/`server`/`handler` consult it. P0 does rotate+dump (cheap, high-value); server/handler god-wiring replacement is deferred to P4. Are you OK that P0–P3 still hand-edit `main.go`'s job block and route mounting for each domain? (Books needs ~3 edits there — acceptable, but it means "add a domain without touching core" isn't fully true until P4.)

8. **Amazon ToS / device-registration fragility for books v1.** The whole ingest hinges on `adp_token` + RSA signing ports of `mkb79/audible`+`mkb79/kindle` against Amazon's private endpoints — unofficial, breakage-prone, and a per-user credential we now encrypt and must rotate/back-up correctly. Is the Amazon-scrobble path solid enough to be the *proof* of the domain contract, or should the contract first be proven on a lower-risk pull source (e.g. Hardcover-read or Goodreads-CSV only) with Amazon added once the seams are validated?

---

*Files/packages cited are from the investigation findings on the current tree: `internal/{jobs,db,stats,widget,widgets,github,ingest,importer,wakatime,curation,spaces,awards,labels,config,auth,identity,server,handler,cache,model}`, `internal/db/{axes.go,wakatime_key.go,github_token.go,dump.go,health.go}`, `cmd/boomtime/{main.go,rotate.go}`, `internal/db/migrations/{00001_initial_baseline,00048_github_token,00049_github_stats_cache}.sql`, `internal/widget/specs.json` ↔ `web/src/features/widgets/specs.ts`. Verify exact signatures before implementing each seam.*