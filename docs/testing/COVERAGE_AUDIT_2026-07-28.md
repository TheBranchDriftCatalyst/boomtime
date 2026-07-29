# Test coverage audit — 2026-07-28

Scope: last ~40 commits, focused on the 7 areas the recent gaka-* work concentrated in.
Method: enumerated the source files, grepped for co-located `*_test.go` / `*.test.ts(x)` /
`web/e2e/*.spec.ts`, and inspected the branches each covers vs. the branches the source
actually has. Read-only audit; no code changed.

## Executive summary

Backend and frontend both have a strong **unit-testing spine** — `internal/db`, `internal/stats`,
`internal/backfill/git`, and the FE `labels/` catalog/conditions/evaluator are unusually well
covered (161 db tests, 73 stats tests, 65 label-catalog fixtures). But the **middle of the
pyramid is thin on the newest gamification wiring**: `internal/db/award_ledger.go` and
`internal/handler/awards.go` — the entire streak-ledger write/read path (POST /awards/log,
GET /awards/streaks, GET /awards/ledger, and the `PeriodBounds` / `GetLabelStreaks` walker) —
have **zero direct test coverage** in Go, and there is no FE test for `useAwardStreaks.ts`,
`formatCondition.ts`, or the streak-badge render on `LabelChip`. The other biggest gaps: no
DST-boundary tests on any tz-bucketed query, no test for the `at`-in-the-future rejection on
`/awards/log`, no HTTP integration test for the backfill job endpoints (only wire-shape +
registry unit tests), and no test for the "Other" bucket regex in `PredicateBuilder`.

E2E coverage: 36 Playwright specs across 7 files, well aligned with recent UI work
(admin sidebar, backfill tab, swagger, dossier, avatar, widgets). None exercise a full round
trip of the streak-ledger from evaluator-fires-in-browser to badge-visible-on-next-load.

## Per-area breakdown

### 1. Gamification / labels

**Source files**
- `web/src/features/publicprofile/labels/conditions.ts` (161 LOC)
- `web/src/features/publicprofile/labels/evaluator.ts` (82 LOC)
- `web/src/features/publicprofile/labels/catalog.ts` (1,287 LOC)
- `web/src/features/publicprofile/labels/tierLabels.ts` (83 LOC)
- `web/src/features/publicprofile/labels/formatCondition.ts` (82 LOC)
- `web/src/features/publicprofile/labels/useAwardStreaks.ts` (154 LOC — read+write hooks)
- `web/src/features/publicprofile/labels/useLabelsCatalog.ts` (79 LOC)
- `web/src/features/publicprofile/labels/LabelChip.tsx` (208 LOC)
- `web/src/features/publicprofile/labels/LabelImage.tsx` (62 LOC)
- `internal/db/award_ledger.go` (346 LOC — `PeriodBounds`, `LogAwards`, `GetLabelStreaks`,
  `ListAwardLedger`, `KindDefaultPeriod`, `ResolvePeriod`, `ValidatePeriod`)
- `internal/handler/awards.go` (190 LOC — 4 endpoints + `parsePositiveInt`)
- `internal/labelcatalog/catalog.go` + drift guard
- `web/src/features/admin/AdminTab.tsx` StreakBackfillSection + ledger inspector

**Tests exercising these**
- `conditions.test.ts` — 30 `it()` blocks; every primitive gets "just over / just under
  threshold" pair. Includes axis-time-sum, punchcard-hour-pct, streak, trend edge cases
  (14-day boundary; prior=0/last>0 → +Infinity).
- `evaluator.test.ts` — 5 cases: empty catalog → [], tier dedupe keeps highest, additive
  tiers across axis-values, rank-desc sort, additive archetypes.
- `catalog.test.ts` — 65 cases: structural invariants (unique ids, rank bands per kind) +
  wave-by-wave fixture coverage of every seeded label (memecore, WH40K, wave-2 patches,
  etc). Most valuable non-tautological guarantees in the FE.
- `tierLabels.test.ts` — 7 cases.
- `LabelChip.test.tsx` — 5 cases (trigger text, data-attr, tooltip on focus, tier line,
  sm/md padding).
- `LabelImage.test.tsx` — 5 cases (smoke).
- `internal/labelcatalog/catalog_drift_test.go` — 3 cases: TS↔Go id parity, no dup ids.

**Coverage gaps** (ranked by risk)
1. **`internal/db/award_ledger.go` has zero Go tests.** `PeriodBounds` (daily/weekly/monthly
   in an arbitrary tz), `GetLabelStreaks` walker (current-period-required, gap detection,
   backward step-by-period), `LogAwards` batched upsert idempotency, `ResolvePeriod` and
   `ValidatePeriod` are all untested. A change to `periodStepBackward` cannot break any
   test.
2. **`internal/handler/awards.go` has zero Go tests.** `AwardsLog` (`at` RFC3339 parsing,
   `at`-in-the-future rejection at +1h, unknown-period filter, lifetime drop), `AwardsStreaks`
   (Cache-Control header, tz resolution from `resolveUserTZ`), `PublicAwardsStreaks` (404 on
   unknown slug, tz of the target user not the viewer), and `AwardsLedger` (label filter,
   limit clamp, parsePositiveInt on non-numeric input) — nothing hits the HTTP surface.
3. **`useAwardStreaks.ts` — no tests.** Both hooks: `useLogAwards` (dedupe-via-ref, gate on
   `isLoggedIn`, drop `lifetime` kinds before POST, no-write on public-slug view), and
   `useAwardStreaks(slug?)` (public vs own endpoint switch, 401 → empty map fallback).
   The mis-attribution guard ("public viewer must not write to own ledger") has no test.
4. **`formatCondition.ts` — no tests.** LabelChip renders `Fires when: ...` from this, no
   assertion that any primitive formats correctly (or that a `not`/`any` composition renders).
5. **LabelChip streak badge (`3x`, amber-500) is not tested.** The AdminTab ledger inspector
   uses `s.streak > 1` for amber; no unit test proves the threshold or the "—" fallback.
6. **Retro-log path in StreakBackfillSection isn't tested at any layer.** The 30-day loop
   posts with `at` set to noon-local; if the server changes the `at`-in-future window from
   +1h to +5m, this quietly starts dropping the current day.

**Rating: 2/5**
Unit story on the pure evaluator is a 5; ledger/streak backend + hooks are a 1. Averaging
where the risk is: the persistence + wire layer that everything else feeds into is the
weakest link.

### 2. Timezone chain (gaka-dg7)

**Source files**
- `internal/handler/timezone.go` (156 LOC — `resolveUserTZ`, GET/PATCH endpoints,
  `trimTimezoneName`)
- `internal/db/user_timezone.go` + `internal/db/rows.go` (`ResolveTimezone`, `SetUserTimezone`,
  `GetUserTimezone`)
- Callers using `h.resolveUserTZ`: `stats.go`, `profile.go`, `widgets.go`, `widget_defs.go`,
  `awards.go` (x2), `scope.go`

**Tests exercising these**
- `internal/db/timezone_test.go` — 4 cases:
  - `TestPunchcard_HourReflectsUserTZ` (non-tautological UTC-vs-Pacific bucket flip).
  - `TestUserActivity_DayBucketReflectsUserTZ` (23:59 PT commit landing on the PT day, not
    UTC's next day).
  - `TestResolveTimezone_3LevelChain` — 5 sub-cases: user > env > UTC, never returns "".
  - `TestSetUserTimezone_RejectsInvalidIANA` — write-path IANA validation.
  - `TestGetTotalTimeToday_UsesUserLocalMidnight` — smoke-only, skips on ambiguous wall-clock.
- `internal/handler/timezone_test.go` — 2 cases:
  - PATCH rejects `Mars/Olympus` → 400 AND no DB write (follow-up GET proves it).
  - PATCH valid → round-trips via GET; PATCH empty clears the pick, effective→UTC.
- `web/src/features/settings/TimezoneCard.test.tsx` — 5 cases: renders effective, manual
  save PATCHes, auto-detect on mount respects "no explicit pick" gate, no-op path
  suppression.

**Coverage gaps**
1. **No DST-boundary test.** A heartbeat at 02:30 on the US spring-forward day (2026-03-08
   at 02:30 PT technically doesn't exist) is not exercised. Existing tests hard-code
   2025-01-15 and 2025-06-15 which are inside stable offsets.
2. **`trimTimezoneName` has no dedicated tests.** Interior whitespace is documented as
   "leave in so LoadLocation rejects" — no test proves LoadLocation actually rejects it
   downstream (only that the leading/trailing trim happens indirectly).
3. **PATCH-triggers-rollup-rebuild side effect is untested.** `UpdateTimezone` calls
   `RefreshRollup(ctx, owner, time.Time{})` + `invalidateOwnerCache(owner)`. If both are
   dropped in a refactor, no assertion fails; the next dashboard load silently serves
   stale UTC-bucketed data.
4. **No integration test that a stats endpoint returns different bounds under two tz.**
   The DB-layer test asserts `GetPunchcard` differs; there's no test that
   `GET /api/v1/stats?...` for the same user & window differs after a PATCH — proving
   the resolver is actually plumbed at the HTTP layer (not just the DB).
5. **`resolveUserTZ` error-path fallback (DB lookup failure → warn+fall through to env)
   is untested.**

**Rating: 4/5**
Excellent DB-layer coverage; the two HTTP-layer tests cover the two most-important
happy/sad paths. Loses one point for missing DST + rollup-side-effect + full-round-trip.

### 3. "Other" chart bucket adaptive cap

**Source files**
- `internal/stats/segment.go` (`capWithOther`, `resourceTopN=12`, `resourceMaxN=40`,
  `otherMaxShare=0.25`, `otherMembersCap=20`)

**Tests exercising these**
- `internal/stats/capwithother_test.go` — 7 cases:
  - Small list unchanged; small list has no Other* fields.
  - Tail collapses correctly; element-wise sums; Other members carried.
  - **Adaptive growth: 40x100s fixture drives topN → 30, verifies share ≤ 25%.**
  - Members cap respected at 20 with a 65-entry fixture.
  - Default N honored when tail is small.
  - Input non-mutation (backing-array sentinel + full-slice-expression trap).

**Coverage gaps**
1. **No test for `topN` capped at `resourceMaxN=40`** — an "Other still dominates at N=40,
   accept it" scenario (say 200 identical-weight entries). Prevents a future regression
   where `topN < len(sorted) && topN < resourceMaxN` bounds get inverted.
2. **No test for the tightening from 30% → 25%** as a regression guard on the constant
   value itself.
3. Cross-package: no test proving stats handlers actually call `capWithOther` on every
   axis they promise to (a caller could forget it on a new endpoint).

**Rating: 5/5**
Best-covered area in the audit. Adaptive growth, members cap, non-mutation, and the
default-N fast path are all pinned.

### 4. Backfill queue + synthetic heartbeats

**Source files**
- `internal/handler/admin_backfill.go` (447 LOC — 8 endpoints incl. WS)
- `internal/db/backfill.go` + `defaultBackfillConfig` + `InsertBackfillBatch`,
  `PreviewBackfillBatch`, `DeleteBackfilledHeartbeats`, `BackfillStatsFor`, `clampBackfillConfig`
- `internal/queue/backfilljobs/registry.go` (in-memory registry + subscribe)
- `internal/backfill/git/{scanner,cluster,walk}.go`
- `cmd/boomtime/backfill.go` (~487 LOC — laptop-side CLI)
- `web/src/features/admin/{useBackfillJobQueue.ts,BackfillTab.tsx}`

**Tests exercising these**
- `internal/db/backfill_test.go` — 8 cases: no-overlap writes-all, real-overlap
  skips-session, prior-backfill-overlap still-writes, delete preserves real, stats counts,
  defaults for new user, roundtrip config, `clampBackfillConfig` forces `backfill:` prefix.
- `internal/backfill/git/scanner_test.go` — 4 cases (author filter, basename repo name,
  since/until clamp).
- `internal/backfill/git/cluster_test.go` — 9 cases (empty, single, gap-merge, gap-split,
  unsorted, top-file, materialize exact-HB, empty-top→placeholder, end-before-start,
  weighted distribution).
- `internal/backfill/git/walk_test.go` — 1 case.
- `internal/queue/backfilljobs/registry_test.go` — 5 cases (enqueue, auto-start/finish,
  IncrementCounts flip queued→running, snapshot filter, subscribe fanout).
- `internal/handler/admin_backfill_test.go` — 2 wire-shape cases only:
  `backfillEvent2json` fields + `backfillConfigPatch` JSON round-trip. **File itself
  says "A real HTTP-integration test would require the full server startup path… This
  file stays wire-only."**
- `web/src/features/admin/useBackfillJobQueue.test.ts` — 4 cases (snapshot, added/updated/
  removed, reconnect backoff).
- `web/e2e/backfill-tab.spec.ts` — 6 cases (stat cards, tooltips, CLI copy block, live
  queue empty state, danger-zone confirm interlock). All admin-gated, skips without
  ADMIN creds/stack.

**Coverage gaps**
1. **No HTTP test for the admin backfill endpoints.** `AdminBackfillEnqueueJob` (rejects
   empty repoName, negative totalCommits, missing queue), `AdminBackfillJobPatch` (owner
   cross-check returning 404-not-403, unknown status → 400), `AdminBackfillJobHeartbeats`
   (payload cap at 4 MiB, empty sessions → 200 empty result, source-tag pulled from
   persisted config not body), and `AdminBackfillDeleteHeartbeats` (source without
   `backfill:` prefix → 400, `?all=true` vs `?source=`) all live only in the source.
2. **Registry retention timer isn't tested.** A `done` job schedules AfterFunc to remove
   after 15min; the customizable `NewRegistryWith` exists explicitly for tests but no
   test uses it.
3. **Registry `broadcastLocked` back-pressure path isn't tested.** The 16-buffer subscriber
   is documented to drop oldest → try again → warn. A slow subscriber test is missing.
4. **The whole `cmd/boomtime/backfill.go` (487 LOC) has no test.** No test that
   `--emails` empty + no server-side authorEmails aborts (correctness guard), no test
   for `skipMatch` glob semantics (basename vs full path), no test for `buildBatchPayload`
   ptr semantics, no test for the auth header shape (`Basic base64(uuid)` post-73395cf
   fix — one of the exact bugs the recent commits landed).
5. **WS handler `AdminBackfillWS` has no test.** Owner-filter at the boundary is a
   critical security guard — untested.
6. **No e2e that actually enqueues a real job** and watches the WS event — the specs
   only verify the empty state.

**Rating: 3/5**
DB-layer + registry + git-scanner all thoroughly unit-tested. Admin HTTP surface (the
most attack-adjacent code) and the CLI (the most user-facing) are close to zero.

### 5. Encryption at rest for Wakatime keys

**Source files**
- `internal/auth/crypto.go` (272 LOC — `LoadKeyFromEnv`, `Encrypt`/`Decrypt`,
  `NewAEADFromBase64`, `EncryptWith`/`DecryptWith`, `ResetForTest`)
- `internal/handler/wakatime_key.go` (save/get/delete + probe)
- `internal/importer/importer.go` `applyKeyOutcome` (save-on-success)
- `cmd/boomtime/rotate.go` + `cmd/boomtime/main.go` (prod hard-fail on missing key)
- `internal/db/dump.go` (backup + restore — includes `encrypted_wakatime_key` column)

**Tests exercising these**
- `internal/auth/crypto_test.go` — 5 cases: round-trip, wrong-key auth failure, tampered
  ciphertext, ErrKeyUnset, ErrKeyInvalid (base64 + wrong length).
- `internal/importer/apply_key_outcome_test.go` — 3 cases against a real Postgres:
  completed+no-401+typed → persists + status=valid; failed+saw401 → status=invalid,
  blob untouched; failed+no-401 → row completely untouched (blob, status, checked_at
  all frozen).
- `cmd/boomtime/rotate_test.go` — `TestRotateSmoke`: seeds 2 users under OLD key, asserts
  wrong-OLD aborts before ANY write (rows still decrypt under real OLD), then happy path
  → all rows decrypt under NEW and NOT under OLD.
- `internal/db/dump_test.go` — heavy coverage of `encrypted_wakatime_key` in backup:
  seed + export + restore round-trip preserves ciphertext exactly; empty-env restore of
  ciphertext-bearing archive → 400 no TRUNCATE; explicit assertion the column is in
  `dumpTables[users]`.
- `internal/handler/wakatime_key_test.go` — 1 case: 5KiB body → 413 (proves probe never
  runs; encryption never runs).

**Coverage gaps**
1. **Prod hard-fail on missing `BOOM_ENCRYPTION_KEY` is untested.** `cmd/boomtime/main.go`
   lines 101-112: `BOOM_ENV=prod` + missing key MUST return the fatal error. No `cmd`
   integration test exercises this control-flow — the one thing that makes gaka-6jm.9
   different from silent-miss.
2. **`SaveWakatimeKey` happy-path with encryption in effect** isn't tested at the HTTP
   layer (413 is the only test). No test that GET returns `{"hasSavedKey": true}` after
   a POST + probe success — and specifically that the plaintext is NEVER echoed.
3. **DELETE `/wakatime_key` is untested** — should NULL the column and flip status. Silent
   drop = user thinks they revoked but ciphertext lingers.
4. **`NewAEADFromBase64` bad-input surface is only tested indirectly** through
   `TestRotateSmoke`. A direct table test would catch a future refactor that stops
   sharing the `ErrKeyInvalid` wrap.
5. **No test that `Encrypt` produces distinct ciphertexts for identical plaintexts across
   users** (documented no-leakage claim — currently relies on `rand.Reader` correctness).

**Rating: 4/5**
Round-trip, tamper-detect, rotation abort, and backup inclusion are all pinned. The
production startup gate + the DELETE endpoint are the notable gaps.

### 6. Achievement conditions & backfill retro-log path

**Source files**
- `POST /api/v1/users/current/awards/log` in `internal/handler/awards.go` (the `at` field
  handling)
- `internal/db/award_ledger.go` `PeriodBounds` (tz-aware bucketing)
- FE StreakBackfillSection in `web/src/features/admin/AdminTab.tsx`

**Tests exercising these**
- **Nothing.** The `at` param, the `+1h` future-rejection window, the RFC3339 parse
  error → 400, and the "unknown periodType filtered silently" branch are all only in
  source. The FE 30-day walk that drives this endpoint has no test either.

**Coverage gaps**
1. Future-`at` rejection (+1h grace) is critical: a stale client with a wrong clock could
   poison the streak walker with a period-start in the future, making `GetLabelStreaks`
   drop that label forever. No test.
2. Missing tests for the "unknown periodType silently dropped" batch behavior — one bad
   item shouldn't 400 the whole batch (documented) but there's no test proving it.
3. Retro-log tz-bucketing: an `at` at 03:00 UTC = 20:00 previous-day PT MUST log to
   yesterday's daily period in PT. This is the exact class of bug gaka-dg7 tried to fix
   for stats; no test proves it holds for awards.

**Rating: 1/5**

### 7. Goals autocomplete "Other" filter

**Source files**
- `web/src/features/goals/PredicateBuilder.tsx` `AxisValueInput` (lines 112-167,
  filter regex on line 145: `/^Other(\s*\(\d+\s*more\))?$/`)

**Tests exercising these**
- `web/src/features/goals/PredicateBuilder.test.tsx` — 336 LOC covering defaults,
  edit-in-place, group-wrap, add/remove children. **No test targets the Other-filter
  regex.** The QueryClientProvider is wired specifically so `AxisValueInput` can render,
  but no test seeds a mock stats response containing `"Other (7 more)"` and asserts it
  doesn't appear in the datalist.

**Coverage gaps**
1. The regex itself is untested: `Other`, `Other (5 more)`, `Other (12 more)` should be
   filtered; `Other Corporation` (legitimate axis value) should survive; case-sensitivity
   is `^Other` (documented) — none tested.
2. The 100-entry `.slice(0, 100)` cap is untested.
3. Fallback: `stats.data` undefined → empty suggestions → `datalist` not rendered — no
   test.

**Rating: 2/5**

## Top 10 recommended new tests

Ranked by impact × risk (defect probability × blast radius).

1. **[unit / Go]** `internal/db/award_ledger_test.go` — `TestPeriodBounds_TzAware`. Table
   test: for each `PeriodType`, assert bounds under UTC vs America/Los_Angeles produce
   different `start` for the SAME `at`. Include the DST-transition day (2026-03-08 for
   spring, 2026-11-01 for fall) — spring-forward gives a 23-hour day, fall-back a 25-hour
   day, and both should still bucket to the local midnight.

2. **[integration / Go]** `internal/handler/awards_test.go` — `TestAwardsLog_RejectsFutureAt`.
   POST `/api/v1/users/current/awards/log` with `at` = now+2h → 400 with body mentioning
   "future"; assert `ListAwardLedger` returns unchanged. Complements the design intent of
   the +1h grace window.

3. **[integration / Go]** `internal/handler/awards_test.go` — `TestAwardsLog_IdempotentInPeriod`.
   POST the same `{labelId, periodType: daily}` twice inside one PT day; assert
   `written=1` on first, `written=0` on second, and `ListAwardLedger` has exactly one row.
   Load-bearing "streak math isn't corrupted by remounts" claim.

4. **[integration / Go]** `internal/db/award_ledger_test.go` — `TestGetLabelStreaks_WalkerGapDetection`.
   Seed 5 daily rows on Mon,Tue,Wed,Fri,Sat (Thu gap), query on Sat under a fixed `at`.
   Assert streak = 2 (Fri, Sat) — proves gap-detection breaks the walk. Also test the
   "current period must have fired" gate: same seed with query on Sun (no row today) →
   label absent from result.

5. **[integration / Go]** `internal/handler/timezone_test.go` — `TestUpdateTimezone_TriggersRollupAndCacheInvalidate`.
   Prime a cached aggregation for a user; PATCH tz; assert the same-query cache key is
   gone AND `hb_rollup_daily` for that user was recomputed. Prevents the silent-stale
   regression the timezone.go comments call out.

6. **[unit / TS]** `web/src/features/publicprofile/labels/useAwardStreaks.test.ts` — 3
   cases: (a) `useLogAwards` with a public-slug context sends NO POST; (b) same-set
   remount doesn't re-POST (deduped by ref); (c) lifetime kinds are filtered out before
   POST.

7. **[unit / TS]** `web/src/features/publicprofile/labels/formatCondition.test.ts` — one
   case per Condition kind that the LabelChip tooltip renders. Non-tautological: a change
   to a template string is caught by an assertion on the exact rendered phrase.

8. **[unit / TS]** `web/src/features/goals/PredicateBuilder.test.tsx` — new
   `describe("axis value autocomplete")` block. Mock the stats query to return
   `["Go", "Rust", "Other", "Other (5 more)", "Other Corporation"]`. Assert the datalist
   contains `Go`, `Rust`, `Other Corporation`, and NOT the two Other buckets.

9. **[integration / Go]** `internal/handler/admin_backfill_test.go` (upgrade) —
   `TestAdminBackfillEnqueueJob_CrossOwner404`. Admin A enqueues job X; Admin B PATCHes
   job X → 404 (not 403). Proves the "no oracle for other admins' job IDs" security
   guard; currently only in source comments.

10. **[e2e]** `web/e2e/streaks-e2e.spec.ts` — login → visit own profile → wait for
    evaluator+useLogAwards to fire → hard reload → assert one or more `data-testid=
    "label-chip"` chips show a `Nx` streak badge. End-to-end guarantee that the
    write-then-read loop works — the one thing NO current test can prove.

## Follow-up beads to file

```bash
bd create --title "gaka-audit-1: unit-test PeriodBounds tz + DST"
bd create --title "gaka-audit-2: integration-test AwardsLog rejects future at (+1h grace)"
bd create --title "gaka-audit-3: integration-test AwardsLog idempotent within period"
bd create --title "gaka-audit-4: unit-test GetLabelStreaks gap detection + current-period gate"
bd create --title "gaka-audit-5: integration-test PATCH /timezone triggers rollup + cache invalidate"
bd create --title "gaka-audit-6: unit-test useAwardStreaks / useLogAwards public-slug guard + dedupe"
bd create --title "gaka-audit-7: unit-test formatCondition per-primitive rendering"
bd create --title "gaka-audit-8: unit-test PredicateBuilder autocomplete filters chart-Other buckets"
bd create --title "gaka-audit-9: integration-test admin backfill job cross-owner isolation (404)"
bd create --title "gaka-audit-10: e2e streak badge appears after evaluator writes + reload"
bd create --title "gaka-audit-11: unit-test prod BOOM_ENV requires BOOM_ENCRYPTION_KEY"
bd create --title "gaka-audit-12: integration-test DELETE /wakatime_key nulls column + flips status"
bd create --title "gaka-audit-13: backfill CLI: skipMatch + auth header shape + emails-required guard"
```
