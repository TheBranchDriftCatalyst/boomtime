# Changelog

All notable changes to boomtime are documented here. This file is generated
by [git-cliff](https://git-cliff.org) from conventional-commit history.
## [1.0.0] - 2026-08-05

### Bug Fixes

- **avatar:** Reap renders orphaned by a restart (gaka-93f.27)
- **stats:** Clamp momentum axis to data extent; punchcard base-case copy
- **auth:** OIDC/RBAC low-severity hardening nits (gaka-93f.19)
- **oidc:** Login page Authentik button, disable password under oidc, exempt static assets from rate limiter
- **oidc:** Route pod->Authentik internally via hostAlias, bypass Cloudflare (gaka-93f.11.2)
- **oidc:** Benign User-Agent so Cloudflare doesn't 403 discovery/token/jwks (gaka-93f.23)
- **web:** Graceful stale-chunk recovery + route errorElement (gaka-93f.22)
- **auth:** OIDC session-lifecycle + nonce + username hardening (red-team gaka-93f.11)
- **grid:** Saved layout is authoritative — stop re-adding removed widgets (gaka-174)
- **profile:** Arasaka grid collapse — tile-fade clobbered RGL transforms (gaka-174)
- **publicprofile:** Route-aware slug in useAwardStreaks (gaka-ie3)
- **avatar:** Scale topLanguage.pct by 100 before rendering
- **publicprofile:** Recompute chip percentages from totalSeconds
- **openapi:** Add spec entries for all 28 remaining router-only routes (gaka-08m + gaka-dam)
- **stats:** Leaderboards test asserts per-owner absence, not whole-DB emptiness (gaka-peu)
- **openapi:** Add spec entries for 7 awards routes (gaka-8tn.2)
- **admin:** Finish gaka-8tn phase 7 wire-up
- **server:** Wire ingest.Register in registerRoutes (gaka-8tn phase 5a follow-up)
- **ingest,handler,testutil:** Finish gaka-8tn phase 5a wire-up
- **stats:** Extend gaka-6ci discriminator across remaining 6 SQL queries + configurable cap
- **stats:** Per-axis discriminator filters null-axis heartbeats out of per-axis pies (gaka-6ci)
- **widgets:** Scrollable + resizable widget sidebar
- **goals:** Autocomplete uses full distinct-value list, not capped /stats bucket
- **testutil:** Switch Harness.T to HarnessT interface for GinkgoT() support
- **viz:** Momentum-axis format was ambiguous on multi-year ranges
- **stats:** Tighten "Other" ceiling from 30% → 25%
- **stats:** Don't let "Other" bucket dominate charts (>30% share)
- **goals:** Filter chart-bucket "Other" out of value autocomplete
- **labels:** Rename patch to terminal-veteran to avoid PK collision
- **tests:** Update axes_test + PredicateBuilder for source axis + useQuery
- **labels:** Correct broken condition JSON on wave-1 seed patches
- **swagger:** Stop double-base64ing access tokens in the FAB mint flow
- **k8s:** Point chibi LLM at qwen3:8b (installed) not qwen2.5:7b (not installed)
- **swagger:** Content-hash the initializer.js src so cache can't get stuck
- **swagger:** No-cache the initializer.js so browsers see updates
- **widgets:** Hide kind-slug suffix in view mode
- **backfill/cli:** Send Basic base64(uuid) auth, not Bearer uuid
- **backfill/cli:** Set explicit User-Agent to bypass CF bot block
- **avatar:** Use direct type-assertion for SSE flusher
- **labels:** Portal tooltip so 256px preview escapes widget card clip
- **backfill:** Rename shadowed 'iter' local var in Scanner.Iter (gaka-vh8)
- **comfyui:** Bump timeouts + stop retrying header-timeouts
- **web/viz:** Normalize oklch chart tokens to sRGB for d3.interpolateRgb
- **build:** Local go.work shadows parent workspace to unbreak gopls
- **comfyui:** Bump timeouts to 600s/720s for chroma-hd on M-series
- **comfyui:** Bump timeouts for chroma-hd (300s header, 360s overall)
- **labelimages:** Async regenerate + batch delete + chroma-hd model
- **comfyui:** Model name uses dashes not underscores (sdxl-illustrious-xl)
- **web:** Regenerate yarn.lock for catalyst-ui@2.5.2
- **goals:** Explicit v: string on Select onValueChange to unblock CI
- **db:** Cast unnest args in get_time_between.sql to fix SQLSTATE 42725 (gaka-6yr)
- **css:** Explicit @source for catalyst-ui dist unbreaks modal centering
- **stats:** Dedupe categories/languages/projects case-insensitively
- **server:** No-cache the SPA shell so deploys aren't held hostage
- **server:** Echo.NewHTTPError requires (code, message) — add message arg
- **server:** Return 404 for missing /assets/* instead of SPA fallback
- **web:** Explicit @testing-library/dom devDep (peer-only under yarn)
- **docker:** Yarn install --ignore-engines to unblock CI on node:22-alpine
- **cmd:** Update main.go create-api-token to match new signature
- **db:** V31 drops raw token columns + collapses dual-path lookups
- **db:** Migration v30 backfills hashed tokens via pgcrypto.digest
- **auth:** Dual-path token list/delete + boot-time raw-token backfill
- **db:** CHECK constraint requiring auth_tokens have raw OR hashed token (v29)
- **importer:** Silence drift on wakatime user_agents ai_model_* fields
- **k8s:** Add BOOM_CORS_ALLOWED_ORIGINS to prod configmap (gaka-n5r gate)
- **k8s:** Wire BOOM_ENCRYPTION_KEY via boomtime-encryption-key Secret
- **web:** Pin catalyst-ui ^2.4.2 + @types/react override + pre-push hook

### Chores

- **deps:** Catalyst-ui ^2.9.0 — auto-scroll marquee trophy shelf (gaka-174.5)
- **refactor:** Phase 0 scaffolding for gaka-8tn domain restructure
- **task:** Add test:coverage + test:coverage:summary targets (gaka-se2)
- **labels:** Remove orphaned drift-guard fixtures from aborted agent attempt
- **bd:** Close gaka-dg7 after landing user-timezone
- **bd:** Checkpoint interactions after closing gaka-dvb
- **bd:** Close gaka-8bz after landing durable image-job queue
- **build:** Standardize on yarn everywhere; drop package-lock.json
- **db:** Squash migrations 00001-00031 into a single baseline
- **hooks:** Auto-activate .githooks via web/ prepare script
- **deps:** Pin catalyst-ui ^2.4.0 + belt-and-suspenders @source
- **deploy:** Point CI + k8s manifests at renamed boomtime repo/main
- **bd:** Checkpoint bead state after gaka-lfc merge session
- **deps:** Add renovate.json for weekly grouped bump PRs
- **deploy:** Pin boomtime image to sha-0735fbe (v0.5.5)

### Documentation

- **labels:** Thematic-criteria sync audit for the 114-row catalog
- Rewrite gamification blog Layer 2 + add server-eval design note (gaka-hc6.7)
- **blog:** Flag Layer 2 stale — evaluator moved server-side (gaka-hc6.7 partial)
- **blog:** Gamification system — arch, extensibility, AI-layer ideas
- **erd:** Refresh from live boomtime_test schema (through v33)
- **css:** Note that belt-and-suspenders @source is removable + schedule

### Features

- **fe:** No-scroll shell + Page POM primitive + Leaderboards pilot (gaka-8qu)
- **ingest:** Substitute WakaTime <<LAST_PROJECT/BRANCH/LANGUAGE>> + backfill command (gaka-4m2)
- **oidc:** Flip BOOM_AUTH_PROVIDER to oidc — Authentik-only login
- **rbac:** Activate BOOM_FEATURE_USER_MODEL in prod (provider stays local)
- **oidc:** Activate OIDC config in prod overlay (provider stays local)
- **auth:** RBAC + user-model substrate + Authentik OIDC + beta onboarding (all default-off)
- **profile:** Date-range changer for the stats window (gaka-174.7)
- **profile:** Gate 3D medallions behind a feature flag; default to chips (gaka-174.5)
- **profile:** 3D award medallions in the labels showcase (gaka-174.5)
- **profile:** Holographic-foil grade badge (gaka-174.6)
- **profile:** Dossier glow-up — dual-mode chrome, theme switcher, WebGL hero (gaka-174)
- **viz:** Tooltip audit + primary-hover enrichment for thin charts (gaka-9pt)
- **publicprofile:** Inline draft editor on /p/:slug for the owner (gaka-ie3)
- **admin:** Smart Condition builder replaces the raw JSONB textarea (gaka-6uf)
- **labels:** Server-side condition JSONB schema validation (gaka-6uf)
- **charts:** 'N% untagged/browsing' subtitle on per-axis charts (gaka-6ci)
- **ux:** Public profile in Spaces group + label period-cadence chip
- **goals:** Smart duration input on the create-goal modal
- **labels:** Delete client-side evaluator; server is authoritative (gaka-hc6.5)
- **labels:** Server-side historical award backfill (gaka-hc6.5.1)
- **labels:** Rewire FE to server-side award endpoints (gaka-hc6.4)
- **labels:** Server-side award evaluation endpoints (gaka-hc6.3)
- **labels:** Port evaluator DSL to Go (gaka-hc6.1)
- **admin:** Award-ledger inspector on labels tab (gaka-mwp-streaks)
- **admin:** Streak backfill tool — replay evaluator over N historical days
- **labels:** Achievement ledger + streak badges + condition explainer
- **tz:** User-timezone aware queries + Settings picker (gaka-dg7)
- **labels:** Axis-time-sum evaluator kind + TERMINAL PURIST + FIELD MEDIC
- **admin:** Synthetic heartbeat inspector on Backfill tab
- **goals:** Autocomplete axis values from user's own stats
- **k8s:** Default server timezone to America/Los_Angeles
- **labels:** Memewarfare wave 2 — expand patch catalog to 30 (gaka-0dw)
- **labels:** Memewarfare wave 1 — patch kind + 6 seed patches
- **admin:** Group labels catalog by kind + sub-group tiers by axis
- **admin:** Promote Admin to a top-level sidebar section (gaka-ebq)
- **swagger:** Schemas as proper data-sheet cards, not floating text
- **k8s:** Upgrade chibi LLM to Cydonia-24B for creative visual prompts
- **backfill:** Distribute heartbeats across files + config tooltips
- **swagger:** Sexier dark theme + fix UNDEFINED chip + fix version fallback
- **swagger:** 10-feature OP upgrade suite + clickjacking gate
- **swagger:** Dark theme + logged-in token minting FAB
- **k8s:** Wire boomtime chibi avatar to LAN Ollama via ollama-shim
- **web/avatar:** Settings > Avatar tab + hero avatar slot (gaka-9v4)
- **avatar:** User chibi portrait pipeline — LLM prompt synth + comfyui render (gaka-9v4)
- **profile:** Corpo dossier tightening pass (gaka-k2p)
- **backfill:** Dump heartbeats.source + rename commitCount → sessionCount (gaka-vh8)
- **backfill:** Git-history backfill CLI + admin UI + overlap-safe insert (gaka-vh8)
- **admin:** Durable in-mem image-job queue + WS stream (gaka-8bz)
- **labels:** Big image preview in chip tooltip + admin editor sheet
- **comfyui,labelcatalog:** Per-request size/model/seed overrides for admin regen
- **labels:** Move catalog to DB + admin CRUD sheet editor (gaka-364.3)
- **admin:** Per-label images table + individual regen (600s each)
- **labels:** LabelChip rich hover tooltip + admin allowlist + WAI model
- **labels:** ImagePrompt on baseline + LabelImage w/ glyph fallback + Admin tab (gaka-myv)
- **k8s:** Route boomtime -> mac comfyui shim via headless Service (gaka-myv)
- **labelimages:** Startup worker + public endpoint + admin regen (gaka-myv)
- **labels:** Memecore label pack — kawaii commander neko paws (gaka-364.1)
- **charts:** Theme-aware chart palette — read --chart-N tokens at draw time (gaka-538)
- **comfyui:** Env-gated shim client + label_images schema (gaka-myv)
- **theme:** Public-dashboard Arasaka variant + catalyst-ui 2.5.2 (gaka-res)
- **labels:** Swap hardcoded hero tagline for label evaluator + LabelsShowcase widget (gaka-364)
- **labels:** MVP catalog with tier + archetype + tribe entries (gaka-364)
- **labels:** DSL types + evaluator + tierLabels helper (gaka-364)
- **goals:** Dashboard widget catalog + tile renderers (gaka-wpb)
- **goals:** Recursive PredicateBuilder + GoalForm modal (gaka-wpb)
- **goals:** Goals Settings tab + list + hooks (gaka-wpb)
- **goals:** Frontend types + API client + query keys (gaka-wpb)
- **goals:** HTTP handlers + route registration + OpenAPI ops (gaka-wpb)
- **goals:** Predicate-tree evaluator with SQL leaves (gaka-wpb)
- **goals:** Schema + DB accessors for user-defined goals (gaka-wpb)
- **dashboard-editor:** Wider widget palette + collapse toggle
- **curation:** Add-to-space dropdown for quick space assignment
- **curation:** Eyeball toggle for pausing curation rules
- **web:** Adopt catalyst-ui 2.5.0 consolidated integration seam
- **curation:** Purge hidden rows via the trashcan icon
- **curation:** Destructively apply rename rules from the UI
- **dashboard:** Composable public profile widgets + drag-drop grid
- **session:** Catalyst-ui migration + profile/wakatime/public-profile + red-team remediation
- **openapi:** OpenAPI 3 spec + embedded Swagger UI at /api/docs (gaka-lfc)

### Performance

- **fe:** Coalesce SPA chunks 115->52 via manualChunks (gaka-93f.23)
- **stats:** Rollup fast-path for Momentum + Leaderboards + CategoryDaily (gaka-o4m)

### Refactoring

- **profile:** Drop redundant '> PROFILE · user@boomtime' hero meta line (gaka-174)
- **web:** Swap 5 custom components to catalyst-ui 2.6.0 primitives (gaka-tu6, closes gaka-b1y.5)
- **router:** Migrate to createBrowserRouter + RouterProvider (gaka-ie3)
- **widget:** Extract shared frame CSS to embed (gaka-8tn.1)
- **openapi:** Extract 1027-LOC inline swagger initializer to embed (gaka-8tn.1)
- Collapse per-domain shim helpers into internal/apihelpers (gaka-8tn phase 8)
- **admin:** Extract admin/backup/import/sources into internal/admin/ (gaka-8tn phase 7)
- **stats:** Extract HTTP surface + dashboardScope into internal/stats/ (gaka-8tn phase 6)
- **curation:** Remove now-orphaned handler-side curation + labels files, wire testutil + server + god type to h.Curation
- **curation:** Extract curation + labels catalog admin into internal/curation/ (gaka-8tn phase 5b)
- **ingest:** Extract heartbeats + workouts + health + entities + explorer into internal/ingest/ (gaka-8tn phase 5a)
- **server,handler:** Drop dead registerAuthRoutes + publicProfile constants (gaka-8tn phase 4 cleanup)
- **identity:** Extract auth + profile + timezone + wakatime_key + avatar into internal/identity/ (gaka-8tn phase 4a)
- **awards:** Extract awards HTTP + backfill + evaluator into internal/awards/ (gaka-8tn phase 4b)
- **widgets:** Extract widgets domain into internal/widgets/ (gaka-8tn phase 3)
- **goals:** Extract goals domain into internal/goals/ (gaka-8tn phase 2b)
- **spaces:** Extract spaces domain into internal/spaces/ (gaka-8tn phase 2a)
- **apihelpers:** Extract shared HTTP helpers into internal/apihelpers/ (gaka-8tn prep)
- **meta:** Extract meta domain into internal/meta/ (gaka-8tn phase 1)

### Tests

- **widgets:** Pin SVG bytes + filename-scrub guard for embed widget (gaka-hsj)
- **avatar:** Bypass MSW for the SSE-stream test (gaka-say)
- **handler:** Resolve gaka-d6x.handler cherry-pick conflicts
- **handler/misc:** Address critique — replace tautologies with real invariants, add cross-user + upstream-leak proofs (gaka-d6x.handler)
- **handler:** Cover misc cluster (19 files) to ~76% (gaka-d6x.handler)
- **handler/ingest:** Address critique — real invariants, cross-user symmetry, body caps (gaka-d6x.handler)
- **handler:** Ingest cluster + shared helpers coverage (gaka-d6x.handler)
- **handler/widgets:** Address critique — real invariants + missing branches + security assertions (gaka-d6x.handler)
- **widgets:** Raise handler widgets cluster coverage to 80%+ (gaka-d6x.handler)
- **handler/auth:** Address critique — kill tautologies, add cross-user + log-scrub + state-machine + probe-URL + malformed-JSON invariants (gaka-d6x.handler)
- **handler:** Auth cluster coverage → 90.7% (auth.go 89.6%, password.go 91.3%, wakatime_key.go 92.7%) (gaka-d6x.handler)
- **handler/awards:** Address critique — pin exact auth codes, add invariant tests (gaka-d6x.handler)
- **handler:** Awards + bigbets cluster coverage sweep (gaka-d6x.handler)
- **handler/curation:** Address critique — real invariants + security gaps (gaka-d6x.handler)
- **handler:** 90%+ coverage on the curation cluster (gaka-d6x.handler)
- **handler/import:** Address critique — tighten status pins, add anti-oracle invariants (gaka-d6x.handler)
- **handler:** Import cluster coverage 28.6→87.1% via ginkgo integration (gaka-d6x.handler)
- **handler/admin:** Address critique — pin invariants, add positive controls, SQL-LIKE + WS shape (gaka-d6x.handler)
- **handler:** HTTP + WS coverage on admin backfill/label-images clusters (gaka-d6x.handler)
- **labels:** Coverage_test.go raises internal/labels from 74.2% to 98.7% (gaka-d6x)
- **logging:** 100% coverage on parseLevel, Setup, teeHandler (gaka-d6x)
- **worker/labelimages:** Address critique — pin real invariants + fill coverage gaps (gaka-d6x)
- **labelimages:** Boost coverage from 52% to 98.4% (gaka-d6x)
- **config:** Raise internal/config coverage 64.1% -> 100% (gaka-d6x)
- **comfyui:** Address critique — pin real invariants, kill tautologies (gaka-d6x)
- **comfyui:** Raise coverage 69.5% -> 99.0% on client (gaka-d6x)
- **importer:** Address critique — pin invariants, close security gaps (gaka-d6x)
- **importer:** Lift coverage from 66% -> 91.2% (gaka-d6x)
- **db:** Salvage db-writer additions from gaka-d6x + fix fixture embed
- **server:** Address critique — pin real invariants, close goroutine leak (gaka-d6x)
- **server:** Raise internal/server coverage 70.4% -> 97.0% (gaka-d6x)
- **queue/imagejobs:** Address critique — pin invariants, drop tautologies (gaka-d6x)
- **imagejobs:** Raise queue/imagejobs coverage 86.8% -> 98.7% (gaka-d6x)
- **apierr:** Address critique — pointer identity, empty-splice, raw-wire injection guards (gaka-d6x)
- **apierr:** Cover Write + BadRequest/NotFound/GenericHTTP + envelope drift (gaka-d6x)
- **backfill/git:** Address critique — pin diff-error partial.Hash + direct buildFilePattern/timestampSteps + blank-email + worktree-file-at-root + symlink-loop + Dockerfile-dead-code (gaka-d6x)
- **backfill/git:** Raise coverage 83.2% → 93.7% via ginkgo specs (gaka-d6x)
- **wakatime:** Address critique — real pointer-independence + empty/dot edges (gaka-d6x)
- **wakatime:** Raise coverage to 95.8% with named-invariant edge cases (gaka-d6x)
- **widget:** Address critique — pin invariants, close security gaps (gaka-d6x)
- **widget:** Fill coverage gaps to 95.8% with invariant-focused tests (gaka-d6x)
- **db:** Drop duplicate openTestDB from wakatime_key_test.go (gaka-se2.9)
- **db:** Security invariants for wakatime_key + importjobs + dump (gaka-se2.9)
- **db:** Remove duplicate openTestDB from dashboard_layouts_test.go (gaka-se2.7)
- **db:** Stdlib tests for dashboard_layouts + widget_links + widget_defs (gaka-se2.7)
- **auth:** Crypto edge + service security invariants (gaka-se2.4)
- **db:** Pyramid coverage for internal/db/health.go (gaka-se2.8)
- **db:** Restore openTestDB(t) stdlib seam alongside openTestDBG (gaka-se2.5/2.6)
- **db:** Cover award_ledger to 92% (gaka-se2.5)
- **db:** Pin API-token security invariants (gaka-se2.6)
- **openapi:** SpecHandler + DocsHandler + Register + UIHandler + strPtr + itoa coverage (gaka-se2.3)
- **model:** 100% coverage on ScrubTail + scrubSegmentTail + hiddenNameSet + HiddenSetsMap (gaka-se2.2)
- **labelcatalog:** 100% coverage on ByID + IDs (gaka-se2.1)
- **dry:** HaveStatus + HaveHeader gomega matchers (gaka-0vp.18 phase 3)
- **dry:** Purge 22 more dead helpers (round 3 — non-*testing.T signatures)
- **dry:** Purge 56 dead stdlib-signature helpers left behind by kill-switch (gaka-0vp.18 phase 2)
- **handler:** Drop manual wakatime_key route registration — testutil.Router() already wires it
- **dry:** Kill-switch complete — every _ginkgo_test.go consolidated (gaka-0vp.17)
- **dry:** Kill-switch batch 1 — 24 stdlib+ginkgo pairs consolidated (gaka-0vp.17)
- **dry:** Fold per-file router builders into testutil.Router() (gaka-0vp.18 phase 1)
- **ginkgo:** Mirror internal/db/rename_merge (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {dump,goals} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {fixture,case_fold} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {observability,regex_all_aggregations} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {spaces,apply_rename} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db/owner_scoping (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {suppression,backfill,purge_hidden} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {toggle_curation,aggregation_invariants} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {labels,timezone} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {active_files,template_rename} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {redact_entities,heartbeats_explore} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {time_window,user_avatars,ai_activity} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {label_images,curation,importjobs,ingest} (gaka-0vp.13)
- **ginkgo:** Mirror internal/db {axes,widgets,sources_health} + G-helpers (gaka-0vp.13)
- **ginkgo:** Bootstrap internal/db suite (gaka-0vp.13)
- **ginkgo:** Migrate goals + auth (final round)
- **ginkgo:** Migrate password + awards_{eval,coverage} (round 6)
- **ginkgo:** Migrate dashboard_layout + widget_defs (round 5)
- **ginkgo:** Migrate handler_test HTTP files (round 4)
- **ginkgo:** Migrate 5 handler_test files + widen harness to HarnessT
- **ginkgo:** Migrate internal/handler pkg-internal files (round 2)
- **ginkgo:** Begin internal/handler migration — suite + 4 simple files
- **ginkgo:** Migrate internal/server (3 files → 3 mirrors)
- **ginkgo:** Migrate internal/testutil (3 files → 4 mirrors)
- **ginkgo:** Migrate internal/stats (9 files → 9 mirrors + suite entry)
- **ginkgo:** Migrate internal/widget (3 files → 3 mirrors)
- **ginkgo:** Migrate internal/importer (5 files → 5 mirrors)
- **ginkgo:** Migrate internal/queue/imagejobs (2 files → 2 mirrors)
- **ginkgo:** Migrate internal/backfill/git (3 files → 3 mirrors)
- **ginkgo:** Migrate internal/worker/labelimages (1 file → 1 mirror)
- **ginkgo:** Migrate internal/auth (3 files → 3 mirrors)
- **ginkgo:** Migrate internal/{logging,queue/backfilljobs}
- **ginkgo:** Migrate internal/{labelcatalog,comfyui} + name-collision note
- **ginkgo:** Migrate internal/{wakatime,cache,config,openapi} (gaka-0vp)
- **ginkgo:** Migrate internal/{labels,apierr,model} (gaka-0vp.1-3)
- **ginkgo:** Bootstrap — labels package as first migrated (gaka-0vp)
- **labels:** Fill remaining evaluator edge cases (gaka-hc6.1.1)
- **labels:** Parametrized coverage sweep — every catalog label fires (gaka-hc6.6)
- **labels:** Integration tests for server-side award endpoints (gaka-hc6.3.1)
- **e2e:** Playwright coverage for recent admin sidebar, swagger, backfill, avatar, dossier features (gaka-dvb)
- **labels:** Race-safe seed assertion in TestLabels_ListSeeded
- **goals:** Add regression coverage for widget renderers (gaka-wpb.1)
- **goals:** Ground gaps in DB/stats/handler goal tests (gaka-wpb.1)
- **stats:** Pin per-day rollup, pagination, DISTINCT dedupe (gaka-oew)
- **stats:** Pin RedactEntities case-fold + owner + ty scoping (gaka-oew)
- **stats:** Pin GetAIActivity invariants — filter, dedupe, empty (gaka-oew)
- **stats:** Pin [start,end] window boundary semantics (gaka-oew)
- **stats:** Pin owner-scoping across every aggregation path (gaka-oew)

### merge

- **ginkgo:** Internal/db ginkgo migration (branch 10/10)
- **ginkgo:** Internal/handler ginkgo migration (branch 9/10)
- **ginkgo:** Internal/server ginkgo migration (branch 8/10)
- **ginkgo:** Internal/testutil ginkgo migration (branch 7/10)
- **ginkgo:** Internal/stats ginkgo migration (branch 6/10)
- **ginkgo:** Internal/widget ginkgo migration (branch 5/10)
- **ginkgo:** Internal/importer ginkgo migration (branch 4/10)
- **ginkgo:** Internal/queue/imagejobs ginkgo migration (branch 3/10)
- **ginkgo:** Internal/backfill/git ginkgo migration (branch 2/10)
- **ginkgo:** Internal/worker/labelimages ginkgo migration (branch 1/10)
- Public profile corpo dossier tightening (gaka-k2p)
- Git-history backfill feature (gaka-vh8)
- **gaka-lfc:** OpenAPI 3 spec + embedded Swagger UI at /api/docs

### polish

- **goals:** DurationInput caption defaults to legend, previews canonical form only when user types a non-canonical shape

## [0.5.5] - 2026-07-17

### Bug Fixes

- **wellness:** Pass required props to DateRangePicker + QueryGate

### Documentation

- Capture deferred followups from v0.5.4 for bd import

## [0.5.4] - 2026-07-17

### Documentation

- **ai:** Reconcile_wakatime_schema_drift prompt (drift JSON -> full capture + chart)

### Features

- **branding:** Boomtime.svg as favicon + app icon + brand chip
- **tokens:** Move API token management from header to Settings tab
- **wellness:** Apple Watch health overlay on WakaTime domain
- **companion:** IOS + watchOS BoomtimeWatch companion app

### Refactoring

- **db:** Case-insensitive aggregation across all axes

## [0.5.3] - 2026-07-13

### Bug Fixes

- **widgets:** Project scope now handles renamed/merged names (gaka-xuc)

### Chores

- **docs:** README self-embed widget + rename db-erd.mmd -> db-erd.md

### Features

- **ai:** Capture wakatime.com AI-assistance fields + Overview AI card (gaka-1l9)
- **import:** Copy drift findings for schema-update feedback loop (gaka-rl6)

## [0.5.2] - 2026-07-13

### Bug Fixes

- **import:** Drop client-side base64 on wakatime api_key; add show/hide toggle (gaka-f2l)

### Features

- **brand:** 'Made by Catalyst Development' attribution (gaka-486)

## [0.5.1] - 2026-07-13

### Bug Fixes

- **security,web:** Cookie-auth logs WS + refresh-loop robustness (gaka-af5, gaka-ia3)
- **k8s:** Pin prod overlay image tag to :gakatime for initial rollout
- **build:** Reconstruct internal/cache TTL package
- **heartbeats:** Badge Space membership for regex rules, not just exact
- **curation:** Hooks-order crash when editing a remapping rule
- **viz:** Dashboard contrast/color/scale sweep + category breakdown + files-are-files

### Chores

- Ignore agent worktrees, sync beads log, add repo descriptor
- **logs:** Log DB query tracer at DEBUG (out of the INFO stream)
- **config:** Enable DB query tracer + slow-query EXPLAIN by default in dev
- Rename dev database test -> boomtime (in-place ALTER, data preserved)
- **compose:** Add restart: unless-stopped to db/app/web

### Documentation

- **query-engine:** Add mermaid diagrams for the two-layer query construction
- Add QUERY_ENGINE.md — deep dive on the aggregation/curation/scope engine
- Refresh all page screenshots (synthwave + Spaces); document Spaces, drop tag refs
- Add WHY.md (origin story) + link from README
- README + DEMO (full-page tour) + ARCHITECTURE + screenshots

### Features

- **settings:** Plugin setup tab with wakatime.cfg snippet (gaka-pi0)
- **heartbeats:** Entity Explorer + search fix + index sizes (gaka-90x)
- **widgets:** Named/saved widget defs table (gaka-3nu)
- **server,web:** /healthz + BOOM_GRADE_* env + welcome modal (gaka-oih, gaka-unq.4, gaka-cly)
- **auth:** Shared user/token creation service (gaka-0tb)
- **tilt:** Tiltfile for local k3s dev (gaka-7q9)
- **k8s,ci:** Fix image path + continuous GHCR publish (gaka-acx)
- **k8s:** Argo-managed manifests + local overlay (gaka-a4d)
- **widgets:** Roll links, per-link hit tracking + origins, drop delete (gaka-hsj follow-up)
- **widgets:** Interactive builder — compose primitives via URL-inline spec (gaka-567)
- **widgets:** 4 more chart twins via primitives (gaka-unq.3)
- **widgets:** DRY primitives + 4 new widget kinds incl. composite (gaka-unq.2)
- **widgets:** Embeddable SVG stats widgets + widget-builder foundation (gaka-hsj)
- **viz:** 'Other' bucket breakdown tooltip (gaka-7m4)
- **release:** Changelog + versioning + GHCR + secrets safety (gaka-o5k)
- **importer:** Detect + persist wakatime.com API schema drift (gaka-unq.1)
- **viz:** Tooltip audit + shared helper across all charts (gaka-9pt)
- **rollup:** Widen hb_rollup_daily to 8 axes (+category/plugin/branch) (gaka-e0l)
- **dashboard:** Persist date-range/time-limit across navigation
- **heartbeats:** Add-to-Space action + Space membership badges on group rows
- **backup:** Save/load the entire database to/from a file (gaka-x0v)
- **web:** Opt-in GA4 analytics module with SPA route tracking
- **logs:** Live server-log viewer tab (WebSocket, reload-durable)
- **sources:** Show heartbeat count per source-health row
- **projects:** Stack 'Total activity' by language
- **curation:** Edit name-remapping rules in place (pencil icon)
- **overview:** Stack 'Total activity' by category
- **curation/spaces:** Autocomplete axis values + live per-strategy preview
- DB observability + source-health panel; track cmd/gakatime entrypoint
- Spaces — rule-based scoped dashboards; remove unused tags
- **projects:** Cross-project active-files table (lynchpins)
- **curation:** Capture/replace-group remappings + Explorer regex/template badge
- **ui:** Synthwave/cyberpunk dark theme (first pass)
- **curation:** Regex name remappings + view-affected + project-extras remap
- **ui:** Collapsible sidebar → icon-only rail
- **projects:** Split into aggregate rail + explicit per-project selector
- **curation:** Non-destructive reversible rename + Settings remappings list + db:mermaid
- **explorer:** Unified TanStack Explorer, import backfill, suppress-from-explorer + curation coverage/tests
- **viz:** Council big-bets — category streamgraph, punchcard, deep-work sessions, momentum grid
- **viz:** Council quick-wins — 6 D3 visualizations + per-project metrics
- Gakatime — Go + React 1:1 port of hakatime, with import, curation, D3 charts and fast rollups

### Performance

- **db:** Pg_trgm + text_pattern_ops for Space regex queries (gaka-o4m)
- **web:** Route-split routes + vendor chunks (gaka-4hv)
- **ingest:** SaveHeartbeats runs atomically in one tx via pgx.Batch (gaka-4sq)

### Refactoring

- **logging:** Return LogHub from Setup, thread through New (gaka-yzs)
- **importer:** HTTP timeout, cancel-ack, unexport internals, drop dead code (gaka-al6)
- **web:** DRY D3 viz layer + import-path updates for feature folders
- Split backend monoliths per-domain; move web to feature folders
- Rename application gakatime -> boomtime
- **charts:** Remove ApexCharts, complete D3 strangler-fig migration
- **sources:** Source health keys on (plugin, machine), not editor/plugin/machine
- **curation:** DRY — one shared RemappingForm for Settings + Explorer

### Tests

- **e2e:** Playwright suite for add-to-Space + membership badges
- **frontend:** Vitest + RTL + msw + mock-socket AIO harness + 70 unit tests
- **backend:** Shared AIO harness + DRY existing tests + P0 gaps + handler HTTP integration
- **curation:** Regex remap integration test across all aggregation paths
- **db:** Isolated gakatime_test database + anonymized real-data fixtures


