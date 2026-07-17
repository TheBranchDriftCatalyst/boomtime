# Deferred followups — captured 2026-07-17

Items surfaced during the `gakatime` branch's Apple Watch companion + Wellness
work (shipped as v0.5.4) that were consciously deferred to keep the release
scoped. Convert each to a `bd` issue once the Dolt beads server is back up:

```bash
# Health-check the dolt server first
bd doctor
# Then, one bd create per item below
bd create --title="…" --description="…" --type=… --priority=…
```

The `bd create` snippets are inline under each item for copy-paste.

---

## 1. CLMonitor protocol seam for geofence testing

The `WiFiPresenceProviding` protocol lets us drive `.wifiOnly` in unit tests
via a fake. `.geofenceOnly` and `.both` still call `CLMonitor` directly, so
those branches have zero test coverage.

**Shape:** a `GeofenceMonitoring` protocol with a Live impl wrapping
`CLMonitor` and an in-memory Fake that lets tests inject a synthetic
`.satisfied` event on demand. `HomePresenceMonitor.startGeofence` becomes
protocol-typed.

```bash
bd create \
  --title="CLMonitor protocol seam for geofence testing" \
  --description="Introduce GeofenceMonitoring protocol so HomePresenceMonitor's .geofenceOnly and .both branches are unit-testable. Same shape as WiFiPresenceProviding (Live wraps CLMonitor.events AsyncSequence; Fake exposes an inject() to synthesize .satisfied events). Update HomePresenceMonitorTests to cover the geofence branches." \
  --type=task --priority=2
```

## 2. HealthKit reader mocking for full ingest tests

`SyncCoordinator.syncOne(kind:)` and the whole HK anchor + upload pipeline
have no end-to-end test coverage. Blocker: `HKHealthStore` /
`HKAnchoredObjectQuery` / `HKObserverQuery` aren't mockable directly.

**Shape:** a `HealthStore` protocol wrapping the four calls we make; Live
delegates to `HKHealthStore`; Fake returns canned `[HKSample]` batches keyed
by `SyncKind`. Coordinator tests then verify anchor commit ordering (2xx
before persist) and retry behavior.

```bash
bd create \
  --title="HealthKit reader protocol seam for coordinator tests" \
  --description="Wrap HKHealthStore + observer + anchored queries behind a HealthStore protocol. Fake returns scripted sample batches per SyncKind. Coordinator tests assert: anchor advances only on 2xx, per-kind isolation (one kind failing doesn't block others), and error surface propagates to Config.recordError." \
  --type=task --priority=2
```

## 3. `@Observable` migration for the companion

The `/swiftui-pro` review flagged `ObservableObject` + `@Published` +
`@StateObject` + `@EnvironmentObject` as the legacy pattern. Systemic across
`Config`, `AppState`, `SyncCoordinator`, `WatchConnector` (both sides),
`WorkoutController`, `WatchSettings`, `HomePresenceMonitor`,
`HomeSetupCoordinator`.

Migrating fixes several downstream problems in one pass:
- Nested-`ObservableObject` staleness in `AboutView` (`isPaired` /
  `isWatchReachable`) and `SyncStatusView` (`isSyncing`)
- The `statusVersion` counter hack in `SyncCoordinator` + the per-row `.id()`
  in `SyncStatusView.row`
- Several `Binding(get:set:)` sites in `SettingsView` and `WatchSettingsView`

```bash
bd create \
  --title="Migrate BoomtimeWatch companion to @Observable" \
  --description="Replace ObservableObject / @Published / @StateObject / @EnvironmentObject with @Observable / @State / @Environment / @Bindable across Config, AppState, SyncCoordinator, WatchConnector (iOS+watchOS), WorkoutController, WatchSettings, HomePresenceMonitor, HomeSetupCoordinator. Eliminates the statusVersion / .id() hack in SyncStatusView, fixes nested-observable staleness in AboutView, and enables cleaner bindings in Settings." \
  --type=task --priority=3
```

## 4. Widget renderers Phase 5

Original plan's Phase 5 (self-embed SVG widgets) never shipped for the
Wellness data. Would add three widget kinds — `weekly-active-minutes`,
`workout-streak`, `resting-hr-trend` — via `internal/widget/render.go` and
matching entries in `web/src/features/widgets/catalog.ts`. The
`TestKindsMatchFrontendCatalog` guard already enforces both-sides parity.

```bash
bd create \
  --title="Wellness widget renderers (weekly-active-minutes / workout-streak / resting-hr-trend)" \
  --description="Add three new widget kinds under internal/widget/render.go with matching catalog.ts entries. Weekly active minutes = mini bar chart, workout streak = badge-style N-day counter, resting HR trend = sparkline. Pure SVG, no external resources. Guarded by TestKindsMatchFrontendCatalog." \
  --type=feature --priority=3
```

## 5. HKWorkoutRoute capture

Wire format already reserves `route: [RoutePoint]` on `WorkoutPayload` and
`workout_details.route` is JSONB in the DB, but the Swift side always
sends nil. Wire up `HKWorkoutRouteBuilder` + `HKWorkoutRouteQuery` for
outdoor activities (`.running`, `.walking`, `.hiking`, `.cycling`) so
routes flow through. Enables a route-viz surface on Wellness later
("typical dog walk paths"). No server changes required.

```bash
bd create \
  --title="HKWorkoutRoute capture for outdoor workouts" \
  --description="On the watch, attach an HKWorkoutRouteBuilder during .outdoor sessions and stream CLLocations into it. On the phone side, HKWorkoutRouteQuery fetches the finished route and PayloadBuilder emits it as [RoutePoint] on WorkoutPayload. Server schema and wire format already accept this — only Swift + Info.plist location strings change." \
  --type=feature --priority=3
```

## 6. SwiftUI Pro code review — remaining items

Nine-item checklist from the `/swiftui-pro` pass, some fixed inline
(deprecated Task.sleep in Uploader, C-style String(format), etc. remain).
Full list in the session review.

- **High**: `Task.sleep(nanoseconds:)` in `Uploader.swift:137` → `Task.sleep(for:)`
- **High**: `.tabItem` in `ContentView.swift:21-29` → `Tab` API (iOS 18+)
- **Medium**: Extract `LiveWorkoutView`'s three `@ViewBuilder` computed properties into `View` structs (recomputes on every 1Hz tick)
- **Medium**: `String(format: "%.0f", kcal)` + `String(format: "%02d:%02d", ...)` in `LiveWorkoutView` → `.formatted(.number)` / `Duration.formatted(.time)`
- **Medium**: Force-unwrap `home!` + `wifi!` in `HomePresenceMonitor.swift:141` → `guard let`
- **Low**: Watch `RootPager` TabView tags without a selection binding — either wire enum-typed selection or drop the tags
- **Low**: `HomeBaseCoordEditor` lat/lon TextFields bound to String — use `TextField(value: format: .number)`
- **Low**: HK builder `beginCollection`/`endCollection`/`finishWorkout` closure completion → async variants (watchOS 10+)
- **Low**: `#Preview { AppState() }` sites — extract `AppState.preview` so Canvas doesn't try to activate WCSession

```bash
bd create \
  --title="SwiftUI Pro review — remaining hygiene fixes" \
  --description="Nine items from /swiftui-pro pass on BoomtimeWatch. Highest impact: Task.sleep(nanoseconds:) in Uploader, .tabItem -> Tab API in ContentView, extract LiveWorkoutView subviews (1Hz recompute), String(format:) numeric formatting, HomePresenceMonitor force-unwraps, watch TabView tags, HK builder closure -> async. Full list in commit 266ebcb doc." \
  --type=task --priority=3
```

## 7. Token metadata: `created_at` + soft-delete for history

`TokensTab` currently shows an "Active" pill for every listed token because
the backend only returns non-expiring tokens. Show creation time + revoked
history requires backend fields:
- `auth_tokens.created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `auth_tokens.revoked_at TIMESTAMPTZ NULL` (soft-delete)
- `ListApiTokens` returns revoked rows too when `?include_revoked=1`
- Frontend Status pill flips between Active / Revoked; adds a "Show revoked"
  toggle

```bash
bd create \
  --title="Token history: created_at + soft-delete for revoked audit trail" \
  --description="Add auth_tokens.created_at + revoked_at columns; change ListApiTokens to include revoked rows behind ?include_revoked flag; StoredApiToken wire type gets createdAt + revokedAt fields; TokensTab renders Active/Revoked pills with a Show revoked toggle. Enables real 'which ones are still active' story the user asked for when we did the header-bar refactor." \
  --type=feature --priority=2
```

---

## Notes

- All items are additive; none block the v0.5.4 release just cut.
- Priorities use bd's 0-4 scale (0=critical, 2=medium, 4=backlog).
- Items 1+2 unblock deeper testing of the companion in the Sim without
  needing a physical device. Item 6 (SwiftUI code hygiene) is the natural
  first sweep once you're back in Xcode.
