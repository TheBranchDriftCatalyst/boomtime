# gakatime Test Pyramid — Coverage Matrices (design, not yet implemented)

> Status: **reviewed, implementation deferred.** Produced by 3 background design agents
> (backend-unit, frontend-unit, e2e). No tests written yet. When we resume, this is the spec.
> Decision still open: scope (P0 / P0+P1 / all), whether to do the 3 enabling refactors, and CI.

Two layers: **unit** (Go + React) and **e2e** (Playwright). Priorities: P0 = smoke/core money-path +
security guards; P1 = important feature/repository behavior; P2 = edges, thin wrappers (push to e2e).

---

## Cross-cutting decisions

- **FE tooling:** Vitest + React Testing Library + jsdom + **msw** (test `api.ts` normalizers &
  error-envelope against real raw hakatime payloads) + **mock-socket** (import WS; msw can't do raw WS).
  Co-locate `*.test.ts(x)`; shared harness in `src/test/` (`setup.ts`, `renderWithProviders.tsx`).
- **BE tooling:** stdlib `go test`, **two tiers**:
  - Tier A = pure/logic (no DB) — runs everywhere with `go test ./...`. Bulk of P0/P1 value.
  - Tier B = repository/DB — keep existing `openTestDB` → `t.Skipf` when Postgres unreachable.
    In **CI**: `postgres:16` service, migrate in a new `internal/db` `TestMain`, set `HAKA_REQUIRE_DB=1`
    to turn silent Skip into Fatal so DB tests can't no-op. Do **not** hard-depend on testcontainers.
- **E2E:** `@playwright/test` in `web/e2e/`, Chromium for P0/P1. **Single-origin binary mode** for CI:
  `task build` → run embedded binary on `:8080` (API+WS same origin). Seed only via `tools/gendata`
  + a small fixed fixture — **never** real wakatime.com. Auth via `storageState` capturing the HttpOnly
  refresh cookie (also covers reload-persistence).

## 3 enabling refactors (recommended before/with test authoring)

1. **FE:** extract inline `useMemo` logic to pure helpers — `lib/bucketing.ts` (`makeGroups`,
   `bucketNums`, `mostActive`; `MAX_CHART_POINTS=62`) + per-chart `mapXxxData()`. Converts ~15 brittle
   render tests → pure-fn tests and removes Overview/Projects copy-paste. Highest leverage.
2. **App:** add `data-testid`s (StatCards, ChartCards, import panel bits, heartbeats nodes, curation
   badges, token rows) + a `data-chart-ready` attribute set post-render (Apex `mounted` / D3 effect).
   Prereq for stable e2e selectors + the "All-time renders ≤5s" freeze guard.
3. **BE:** `TestMain` in `internal/db` that migrates, so Tier B self-provisions.

## Proposed build order

P0 pure BE (`capWithOther`, start-clamp/`fillMissing`, `cache.TTL`, date-defaults, leaderboard ordering,
`Hub`) → P0 DB (`RefreshRollup`/`RecomputeGaps`) → FE P0 pure/hook + bucketing extraction → e2e P0 smoke
(~15 specs) → P1 (DB repo tests, feature e2e) → P2 mostly to e2e.

---

# Layer 1a — Backend Go unit matrix

**Existing (9 files):** `auth/auth_test.go`, `stats/stats_test.go`, `db/curation_test.go`,
`db/heartbeats_explore_test.go`, `db/importjobs_test.go` (has shared `openTestDB`/`testDSN`),
`handler/curation_test.go`, `handler/heartbeats_explore_test.go`, `importer/importer_test.go`,
`wakatime/wakatime_test.go`. DB-backed ones skip gracefully without Postgres.

### auth (pure)
- `ParseAuthHeader` Basic/empty/Bearer/`"Basic "`-empty/space-trim — **P0** (partial exists)
- `HashPassword`/`VerifyPassword` distinct-salt + empty-pw — P1 (round-trip/len/wrong-pw exist)
- `ParseRefreshCookie` `=`-in-value, positional, empty header — P1 (present/missing exist)
- `NewRawToken`, `ToBase64` — P2

### stats (pure) — the money path
- `CountDuration` `([1,2,3,10,21,22,33,100,104,109],5)==12`, multi-group boundaries, isolated points — **P0** (partial)
- `CompoundDuration` week unit, boundary carries, `45s`→"" — **P0** (partial)
- `ToStatsPayload` start-clamp, fillMissing gap day, nub order, editors/platforms/machines — **P0** (partial)
- **`capWithOther`** ≤12 unchanged; >12 top-12 + "Other (N more)" element-wise summed daily arrays — **P0, ZERO coverage**
- `ToLeaderboardsPayload` top-20 cap, tie-break (value desc, name asc), omit-empty-lang — **P0** (partial)
- `genDates`/`truncateDay`/`sameDay` — P1; `ToProjectStatistics` (weekday7/hour24 uncapped) — P1; `ToTimelinePayload` <60s drop — P1

### db/curation
- `exclusionPredicate` shape/empty, `AnyHidden`, `renameMap.apply` (nil/absent/new-alloc/no-cross-mutate) — **P0** (apply is N)
- `ApplyRename` project merge+delete, badge NOT EXISTS collision, reject `day` axis — P1 DB (partial)
- Rule CRUD dedupe-upsert, `loadRenameMap`, `HiddenValues`/`LoadHiddenSets` — P2 DB

### db/sessions
- `unixToTime` (P0 pure), `injectAfter` anchor-absent no-op (**P0**, exists), `numToFloat` (P1 pure)
- `RecomputeGaps` first-beat-NULL, out-of-order `-infinity` anchor, `since` bound — **P0 DB**
- `RefreshRollup` gap>900→0, ==900 boundary, NULL→'Other', days≥since — **P0 DB**
- `SaveHeartbeats` canonicalization (entity/type rename, cursorpos int→decimal string) — P1 DB
- `GetUserActivity`/rollup hide exclusion (rollup drops plugin) — P1 DB (exists)
- tokens `GetUserByToken`/`CreateAccessTokens`/`DeleteTokens`, `InsertUser` dup→(false,nil), `SetTags`, `GetTotalTimeBetween` reversed — P1/P2 DB

### db/heartbeats_explore
- `ExploreColumn` whitelist + injection reject (`"1; DROP TABLE"`), `buildFilterClause` nil→IS NULL + arg numbering — **P0** (exists)
- `GroupHeartbeats` day bucket/NULL/truncation-flag — P1 DB; `ListHeartbeats` page<1→1, dep-array — P2 DB

### db/importjobs + derived (DB)
- `GetRunningJobByOwner` (P1, exists), `UpdateJobProgress`/logs afterId + limit clamp — P2
- `CancelJob`/`MarkRunningJobsFailed`/`MarkJobRunning` COALESCE, `GetDerivedStatus`, `ResyncDerived` — P2

### importer + wakatime (pure)
- `genDateRange`/`DayRange`/`TotalDays` same-day→2 (+1 semantics) — **P0** (exists)
- `Hub` Subscribe/Publish/Unsubscribe: buffer-full(64) silent-drop, unsubscribe-close, no-sub no-op — **P0, N**
- `MapState` unknown→JobPending default — P1 (known states exist); `convertForDB` UA/machine-fallback — P1
- `Worker.Cancel`/ctx-cancel→finishCancelled, RecoverInterrupted — P1 DB+ctx
- `UserAgentInfo` platform=1/editor=3/plugin=4 (**P0**, exists), `LanguageFromEntity` ext map + trailing-dot→nil (**P0**, exists)

### handler (helpers via httptest, no DB)
- `collectExploreFilters` reject non-whitelisted/raw-col→400, empty→IS NULL — **P0** (exists)
- `defaultWeekRange`/`defaultMonthRange`/`parseTimeParam` 4-way branch, tz→UTC — **P0, N**
- `statsCacheTTL` (neg→30s, 0→disabled), `cacheKey` (time→Unix), `parseTimeParam`, `queryInt64`/timeLimit=15, `effectiveImportToken` — P1
- Full request→DB→envelope flows (Login/Register/Stats/Import/WS) — **P2 → push to e2e**

### apierr / config / cache / logging (pure)
- `cache.TTL` Get/Set expiry+evict+InvalidatePrefix (inject `now`) — **P0, N**
- `apierr.*` codes (MissingAuth **400** not 401, InvalidToken 403…) — P1
- `config.Load` defaults + WakatimeAPIKey precedence (WAKATIME_API_KEY else HAKA_REMOTE_WRITE_TOKEN), `getEnvInt/Bool` — P1
- `logging.parseLevel` — P2

**Top gaps:** capWithOther, start-clamp/fillMissing, RefreshRollup/RecomputeGaps, cache.TTL,
date-range defaults, Hub pub/sub, SaveHeartbeats canonicalization, leaderboard cap/tie-break.

---

# Layer 1b — Frontend unit matrix (~50 units)

Test-type key: pure / hook (renderHook) / cmp (RTL) / msw (API-boundary).

### lib/api.ts (msw)
- `getTokens` tknId→id; `getTimeline` timelineLangs→langs (+ absent→`{}`); `getLeaderboards` lang→languages (+ missing→[]/{}) — **P0**
- `buildUrl` drops undefined/null/"" but keeps `0`, no trailing `?`, encoding — **P0**
- `request()` envelope: `.message`→`.error`→statusText→"Request failed"; auth header only when token; ApiError.status/payload — **P0**
- curation/import calls, path-param encodeURIComponent — P1/P2

### lib/auth.ts (pure)
- `authHeader()` → `Basic <token>` **verbatim, NOT re-encoded** (old 403 bug, unguarded) — **P0**
- `update`/`getSnapshot` mapping, `isLoggedIn` both-fields — **P0**; `tokenExpiry` ISO→ms, subscribe/emit, broadcastLogout — P1

### hooks
- `useTimeRange` presets, numDays=ceil(|Δ|/86.4e6) DST-safe, defaults 15/15 — **P0** (hook, fake timers)
- `useAuth` bootstrap (resolve/reject/unmount-cancel) — **P0**; refresh loop 60s tick, 5-min margin, fail→clear+broadcast — **P0**
- `useCuration` invalidates exactly `["curation"]` + 7 DEPENDENT_KEYS — **P0**
- `useImportJobSocket` snapshot/log/progress/state (cap 2000), reconnect backoff min(500·2^n,15000), terminal→no-reconnect — **P0** (mock-socket, fake timers)
- `useAxisValues` groups→options, filter null/"" buckets, sort desc, enabled when axis≠null — P1; `isTerminalState` — P1

### Overview/Projects data-prep (currently inline — extract!)
- bucketing groups (n≤62 / >62 size=ceil(n/62) / last min(size,n-i); boundaries 62/63/1000) — **P0**
- bucketNums/chartDailyTotal sum + `??0`; most-active excludes `startsWith("Other (")`, empty→"-" — **P0**
- bucketItems totalDaily re-bucket, chartDates first-of-bucket — P1

### charts (extract `mapXxxData()`)
- HeatmapChart top-7 + append all "Other (" rows, Y-truncate 12 — **P0**
- FileBarChart drop "Other (" + hours>0, slice(0,10), parent/filename label — **P0**
- PieChart drop <60s, D3 label only ≥5%; HourBar 24-array addTimeOffset; Radar parseInt→7-array; Timeline flatten; Column pair — P1/P2
- switcher (7 files) renderer→impl — P1; base.ts formatters — P2

### viz renderer + theme (pure store)
- `readStoredRenderer` key `gakatime-renderer`, invalid/SSR→"apex"; toggle persists — P1
- `readStoredTheme` `gakatime-theme`, system→matchMedia, default **dark**; `applyThemeToDocument` `.dark` class — P1/P2

### utils
- `secondsToHms` (52320→"14 hrs 32 min", singular, <60, 0/null→"0 mins") — **P0**
- `formatElapsed` (H/M/S zero-pad, to=null→now, neg→0) — **P0**
- `daysBetween`, `truncate`, `addTimeOffset` wrap — P1; formatDate/removeDays/removeHours — P2

### components
- combobox creatable (`canCreate` = creatable & non-empty & !exactExists, case-insensitive) — **P0**
- combobox filter case-insensitive substring, cap 200 — P1
- DateRangePicker "All time" ≥3650 threshold, ALL_TIME_START 2000-01-01 — P1
- RenameGroupDialog submit `{axis,action:"rename",matchValue,newValue}`, no-op empty/same — P1

**Gaps/risks:** inline data-prep (extract = highest leverage), copy-paste MAX_CHART_POINTS/isOther/switcher,
WS has no standard mock (mock-socket), timer/timezone flakiness (pin clock + mock getTimezoneOffset),
the `Basic <token>` invariant is a P0 first-batch test.

---

# Layer 2 — E2E (Playwright) scenario matrix (~50 specs, ~15 P0 smoke)

**Setup:** `web/e2e/{fixtures,helpers,specs}` + `global-setup.ts` (db reset → create-user → create-token →
`gendata --num 120` over last 60 days + fixed fixture) + `auth.setup.ts` (storageState w/ cookie).
`playwright.config.ts` webServer boots `bin/gakatime`, `trace:'on-first-retry'`, `retries:2` in CI.
**Prereq:** add data-testids + `data-chart-ready` (0 exist today).

### P0 smoke path (merge gate)
Auth A1 register / A3 login / A4 wrong-pw / A5 refresh-persistence / A6 logout →
Overview O1 (4 cards + all charts ready) + **O3 all-time renders ≤ budget (freeze guard)** →
Projects P1 (all charts) → Heartbeats H1 (root groups) →
Import I1 start(server-key/token-blank) / I2 WS logs stream / I4 cancel / **I5 reload re-binds (durability)** →
Curation C1 (hide → gone from Overview/Projects, **still in Explorer**) →
Strangler S1 (Apex→D3 swaps all) → Theme T1 (dark default).

### P1/P2 highlights
- Auth: register validation, cross-tab logout (2 pages/context), registration-disabled (403), route guard
- Import: progress advances, history + past-run logs, detect-range, idempotent re-import (no dupes), WS reconnect
- Overview: presets, tag filter, timeout dropdown, top-N + "Other"
- Projects: switch project, all-time, tags/commits/badge modals, tag view
- Heartbeats: group chips multi-level, source axes, lazy drill, leaf table + JSON drawer, Table/JSON, entity search, empty range
- Curation: unhide, hide source, rename/merge (all instances) ; Derived: in-sync + sizes, Resync
- Strangler: persist across reload, both renderers × light/dark ; Tokens: create/list/rename/delete

### Flakiness mitigations
`data-chart-ready` waits (never sleeps); wait on outcomes not timers for WS; fresh context per spec file
(in-memory token + cookie + localStorage bleed); fixed fixture for exact-value assertions, gendata volume
for render/no-freeze; serial/ isolated entities for mutating specs; `waitForResponse` on `/stats` etc.
(not networkidle — import polls 15s, derived 30s); `grantPermissions(['clipboard-*'])` for copy specs.
