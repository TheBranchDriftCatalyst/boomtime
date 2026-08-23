# FE Spike: Shared Page Object Model (POM) + No-Scroll Shell

**Status:** SPIKE / design proposal. Prototype primitives landed (unwired); no
live pages migrated. `cd web && yarn typecheck` passes with the new files.

**Scope:** formalize a shared `<Page>` composition model across every route, make
the app shell own the viewport (no global scroll — only designated regions
scroll), shim a transparent tile drag-and-drop layer into dashboard pages,
unify dashboard-config persistence onto ONE store, DRY up per-page layout
duplication, and reorganize the Admin section for CX.

---

## 0. Executive summary (10 lines)

- **Shell today grows past the viewport** (`AppShell` uses `min-h-screen` + an
  inner `<main overflow-y-auto>`), so tall pages scroll the body and the
  sidebar/header can scroll away. Fix: pin the shell to `100dvh` with a CSS grid
  and make ONE region (`<Page.Content>`) the sole `overflow-y-auto` box.
- **Every page hand-rolls the same skeleton**: `<div><PageToolbar title>…range
  picker…</PageToolbar><div className="space-y-6">…</div></div>`. That repeated
  chrome is the biggest duplication. Fix: a shared `<Page>` POM
  (`Page.Header` / `Page.Body` / `Page.Content` / `Page.Aside`) wrapping the
  existing catalyst-ui `<PageToolbar>` — extend, don't fork.
- **DnD + layout persistence already exist and are reusable.** The public-profile
  editor is built on an isolated grid primitive (`@/lib/grid`,
  `react-grid-layout@2.2.3`) persisted through a **generic `(owner, scope) →
  JSONB` table** (`dashboard_layouts`) and REST endpoints
  (`/api/v1/users/current/dashboard/:scope`). We reuse BOTH: no new DnD lib, no
  new table — just add scope keys (`overview`, `space:<id>`) to the server
  allowlist and wrap the grid in a transparent, edit-toggled overlay.
- **Admin CX:** four flat tabs (Users/Labels/Backfill/Logs) where Labels is a
  63 KB monster; regroup into *Operations* vs *Configuration* and move
  cross-cutting user-facing controls out of Admin.

---

## 1. Current-state map

### 1.1 The shell + scroll model

Entry: `web/src/main.tsx` → `createBrowserRouter` (data router, migrated in
boom-ie3 to unlock `useBlocker`) with a single `RootLayout` route whose child is
a `path:"*"` catch-all lazy-loading `AppRoutes` (`web/src/app/App.tsx`). The
classic `<Routes>` tree lives in `AppRoutes`; `/app/*` renders inside
`<AppShell>` behind `<ProtectedRoute>`.

`web/src/layout/AppShell.tsx` (verbatim):

```tsx
<div className="flex h-full min-h-screen bg-background">
  <Sidebar … />
  <div className="flex flex-1 flex-col overflow-hidden">
    <HeaderBar … />
    <main className="flex-1 overflow-y-auto p-6">
      <Outlet />
    </main>
  </div>
</div>
```

**Scroll model today:** `min-h-screen` lets the flex root grow *past* the
viewport. In practice `<main>` is the scroll region (it has `overflow-y-auto`),
which is close to the desired behavior — BUT the `min-h-screen` on the root
means there is no hard `100vh` cap, and pages that hand-roll their own
`h-screen`/`h-[60vh]` children (e.g. `PageFallback`, `RootRedirect`) fight it.
The shell height is emergent, not owned. There is **no shared `<Page>`
primitive** enforcing where scroll happens — each page decides.

Chrome pieces: `Sidebar.tsx` (collapsible rail, `NAV` array + Spaces group +
Admin link + public-profile link), `HeaderBar.tsx` (theme switch + user menu),
`useCollapsedSidebar.ts` (persisted collapse state).

### 1.2 Per-page duplication (the POM target)

Every feature page repeats the **same three-part skeleton**: an outer `<div>`, a
catalyst-ui `<PageToolbar title=…>` holding a near-identical set of toolbar
controls, and a content wrapper (usually `space-y-6` / `flex flex-col gap-6`).
`<PageToolbar>` is already shared (good) — but the *composition around it* is
copy-pasted 11×.

| Page (`web/src/features/…`) | Toolbar title | Range picker | TimeLimit | Widgets panel | Content wrapper | Notes |
|---|---|---|---|---|---|---|
| `overview/OverviewDashboard` | `Overview`/scoped | ✅ | ✅ | ✅ `user`/`space` | `space-y-6` | reused by SpaceView |
| `projects/Projects` | `Projects` | ✅ | ✅ | ✅ `project` | bespoke rail+detail | own scroll-into-view |
| `leaderboards/Leaderboards` | `Leaderboards` | ✅ | — | — | cards | |
| `heartbeats/Heartbeats` | `Heartbeats` | ✅ | ✅ | — | tab strip + panels | explorer/entities tabs |
| `wellness/Wellness` | `Wellness` | ✅ | — | — | `flex flex-col gap-6` | |
| `import/Import` | `Import` | — | — | — | panels | live socket |
| `settings/Settings` | `Settings` | — | — | — | tab strip (`?tab=`) | |
| `changelog/Changelog` | `Changelog` | — | — | — | version chip | Settings tab |
| `logs/Logs` | `Logs` (or `embedded`) | — | — | — | table | double-duty: standalone + admin-embedded |
| `admin/AdminPage` | `Admin` | — | — | — | NavLink tab strip + `<Outlet>` | |
| `spaces/SpaceView` | via OverviewDashboard | ✅ | ✅ | ✅ | manage panel | wraps OverviewDashboard |

Recurring sub-patterns that the POM absorbs:
- **Toolbar trio** — `<PageToolbar>` + `<DateRangePicker>` + `<TimeLimitDropdown>`
  (+ `<WidgetsPanel scopeType/scopeRef>`) appears on Overview, Projects,
  Heartbeats, SpaceView with identical wiring off `useTimeRange()`.
- **Tab strips** — Admin and Settings each re-implement a horizontal
  mono-uppercase tab strip with an active underline (Admin via NavLink child
  routes, Settings via `?tab=`). Two implementations of one visual.
- **Scoped-title convention** — `title` + optional `toolbarActions` slot
  (OverviewDashboard already generalized this; the POM makes it universal).

### 1.3 Existing dashboard-layout / widget / DnD assets (REUSABLE)

This is the pleasant surprise: **the DnD + layout-persistence system already
exists**, built for the public profile, and was deliberately designed generic.

**Client grid primitive — `web/src/lib/grid/`** (an "npm-package-in-waiting",
boomtime-agnostic; barrel `index.ts`):
- `DraggableGridLayout` — wraps `react-grid-layout@2.2.3` `Responsive`. Props:
  `instances: WidgetInstance[]`, `storage: StorageAdapter`, `editable: boolean`,
  `cols`, `rowHeight`, `seedLayout`. **`editable` toggles static vs
  draggable/resizable** and shows/hides the remove-X — this is exactly the
  "transparent when not editing" behavior we need.
- `types.ts` — `GridLayoutItem {i,x,y,w,h,view?,hidden?}`, `WidgetInstance`
  (opaque `render(ctx)`), `StorageAdapter {load,save}`.
- `storage.ts` — `memoryAdapter`, `localStorageAdapter`. `WidgetHost`,
  `ChartToggle`, `layout-evolution` (`buildDefaultLayout`, `mergeLayouts`).

**Widget catalog — `web/src/features/widgets/catalog.ts`**: `WIDGET_CATALOG`
entries keyed by `kind`, each with `dashboardScopes?: ("profile" | "overview" |
"projects")[]`, `defaultLayout {w,h}`, `views`, `defaultView`. The scope tags
are already there for a multi-page rollout — only `profile` is wired today.
`WidgetRenderer` maps `kind` → chart.

**Persistence — client:** `web/src/features/publicprofile/dbStorageAdapter.ts`
(`dbStorageAdapter(scope)` / `hydratedStorageAdapter`) implements
`StorageAdapter` against `api.getDashboardLayout(scope)` /
`putDashboardLayout(scope, {cols,widgets})` / `deleteDashboardLayout(scope)`.
The composed editor is `ProfileEditor.tsx` (draft-then-save, dirty guards via
`useBlocker` + `beforeunload`, palette of catalog − in-layout).

**Persistence — server:**
- Table `dashboard_layouts` (`internal/db/migrations/00032_dashboard_layouts.sql`):
  `id UUID, owner TEXT→users, scope TEXT, layout JSONB, updated_at, UNIQUE(owner,
  scope)`. **`scope` is free-form by design** — the migration comment says it
  exists so the same table backs the authed Overview + per-page dashboards
  "without another migration."
- Accessors `internal/db/dashboard_layouts.go`: `Get/Set/DeleteDashboardLayout`
  (upsert on conflict). **The DB layer does not restrict scope at all.**
- Endpoints `internal/spaces/dashboard_layout.go` (routes `routes.go:29-31`):
  `GET/PUT/DELETE /api/v1/users/current/dashboard/:scope`, envelope
  `{"layout": …}`, 4 KiB cap. **Scope is gated by an allowlist**
  `dashboardLayoutScopes = {"public_profile"}` (`dashboard_layout.go:32`) — the
  ONLY thing blocking reuse for other pages.

**Spaces** (`internal/spaces`, `internal/db/spaces.go`): a Space = named
user-scoped filter over heartbeats, config stored as `space_rules` rows
(relational, not JSON). A space does NOT yet own a `dashboard_layouts` row, but
the schema supports `scope = "space:<id>"`.

**Assessment:** a reusable per-scope layout store already exists. No new table,
no new DnD dependency. The work is (a) open the server scope allowlist, (b) wrap
the existing grid primitive in a uniform overlay, (c) generalize the existing
`dbStorageAdapter` into a shared hook.

---

## 2. Proposed POM

### 2.1 Component tree

```
AppShellNoScroll                 ← owns the viewport: grid, h-dvh, overflow-hidden
├─ Sidebar         (col 1, rows 1–2)   ← unchanged
├─ HeaderBar       (col 2, row 1)       ← unchanged, never scrolls
└─ <main> (col 2, row 2, min-h-0, overflow-hidden)
   └─ <Outlet/> → a route that renders:
      Page                              ← h-full min-h-0 flex-col
      ├─ Page.Header  (shrink-0)        ← wraps catalyst-ui <PageToolbar>
      │     title + subtitle + actions (range picker, widgets panel, tabs)
      └─ Page.Body    (min-h-0 flex-1)  ← horizontal split
         ├─ Page.Content (flex-1, overflow-y-auto)   ← THE ONLY SCROLLER
         └─ Page.Aside   (optional, overflow-y-auto) ← right rail
```

Key invariant: **exactly one `overflow-y-auto` on the vertical axis per page**
(`Page.Content`; `Page.Aside` scrolls independently when present). The shell and
every ancestor use `overflow-hidden` + `min-h-0`. The page body never scrolls;
horizontal overflow (wide tables/charts) is each component's own
`overflow-x-auto` container, never the shell.

### 2.2 No-scroll shell — CSS approach

Grid, not nested flex, so the sidebar can span both rows while header + content
stack in the second column, and the content cell gets a real `min-h-0` via
`minmax(0,1fr)`:

```
grid h-dvh overflow-hidden
  grid-cols-[auto_1fr]                 // [sidebar][content]
  grid-rows-[auto_minmax(0,1fr)]       // [header][content]  minmax(0,…) == min-h-0
sidebar: row-span-2
header:  col-start-2 row-start-1
main:    col-start-2 row-start-2 min-h-0 overflow-hidden
```

`h-dvh` (dynamic viewport height) over `h-screen`: on mobile it excludes the
collapsing browser chrome, so the grid never hides behind the URL bar.

### 2.3 Primitive sketch (shipped as prototype)

`web/src/layout/Page.tsx` — `Page` + namespaced `Page.Header/Body/Content/Aside`.
`Page.Header` renders the existing `<PageToolbar>` (title/subtitle/actions) so
typography and action-wrapping are byte-identical to today; it only adds
`shrink-0` + padding so it sits outside the scroll region.

`web/src/layout/AppShellNoScroll.tsx` — slot-based (`sidebar`/`header`/`children`)
grid shell implementing §2.2. Slot-based on purpose: the prototype stays
decoupled from auth hooks/dialogs; the real migration folds this grid into
`AppShell.tsx` keeping its existing Sidebar/HeaderBar/`CreateSpaceDialog`/
`WelcomeModal` wiring.

### 2.4 Usage example (Overview, after migration)

```tsx
export function OverviewDashboard({ space, toolbarActions, title = "Overview" }: Props) {
  const tr = useTimeRange();
  // …queries unchanged…
  return (
    <Page>
      <Page.Header title={title}>
        {toolbarActions}
        <WidgetsPanel scopeType={space ? "space" : "user"} scopeRef={space ?? ""} />
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker numDays={tr.numDays} onPreset={tr.setDaysFromToday} onRange={tr.setRange} />
      </Page.Header>
      <Page.Body>
        <Page.Content>
          <QueryGate query={statsQuery}>{(stats) => (
            <div className="space-y-6">{/* …unchanged charts… */}</div>
          )}</QueryGate>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
```

The diff per page is small: swap the outer `<div>` + `<PageToolbar>` for
`<Page>` + `<Page.Header>`, wrap the body in `<Page.Body><Page.Content>`. No
query, no chart, no toolbar-control change.

### 2.5 Playwright benefit (secondary)

Once every page is `<Page>` with stable landmarks (`data-testid="page"`,
`page-header`, `page-content`), e2e page-objects share one base class:
`header()`, `content()`, `toolbar()` selectors resolve identically across
routes, replacing per-page selector guesswork. Primary goal is the APP model;
this falls out for free.

---

## 3. Transparent DnD layer

Goal: a single wrapper that adds tile drag/drop to ANY dashboard page,
**transparent (visually inert) outside edit mode**, uniform across pages.

**Library:** reuse `react-grid-layout@2.2.3` via the existing `@/lib/grid`
primitive. It is already a dependency, already isolated, already powers the
profile editor. **No new DnD dep** (dnd-kit / react-dnd would be a
regression — flag any such addition).

**Transparency mechanism (already built in):** `DraggableGridLayout`'s
`editable` prop drives `static: !editable` per RGL item and hides the remove-X.
When `editable={false}` the grid renders as plain positioned tiles — no drag
handles, no resize handles, `onLayoutChange` early-returns. So "transparent when
not editing" is literally the existing read-only mode.

**Proposed wrapper — `DashboardCanvas`** (sketch; a follow-up primitive, not in
this spike's shipped files):

```tsx
// web/src/features/widgets/DashboardCanvas.tsx  (SKETCH)
interface DashboardCanvasProps {
  scope: string;                       // "overview" | "space:<id>" | "projects" | …
  catalogScope: DashboardScope;        // which catalog entries apply
  data: unknown;                       // payload passed to WidgetRenderer
  editable: boolean;                   // driven by a shell-level "edit layout" toggle
}

export function DashboardCanvas({ scope, catalogScope, data, editable }: DashboardCanvasProps) {
  const qc = useQueryClient();
  const storage = useMemo(
    () => dbStorageAdapter(scope, { onSaved: () => qc.invalidateQueries({ queryKey: qk.dashboardLayout(scope) }) }),
    [scope, qc],
  );
  const instances = useMemo(
    () => WIDGET_CATALOG
      .filter((e) => (e.dashboardScopes ?? []).includes(catalogScope))
      .map((e) => ({
        key: e.kind, displayName: e.title, defaultLayout: e.defaultLayout,
        views: e.views, defaultView: e.defaultView,
        render: ({ view }) => <WidgetRenderer kind={e.kind} view={view} data={data} />,
      })),
    [catalogScope, data],
  );
  return <DraggableGridLayout instances={instances} storage={storage} editable={editable} cols={12} />;
}
```

- **Read path (default):** `editable={false}` → static tiles, zero interaction
  affordances, identical to a normal render. This is the "shimmed into every
  dashboard page, transparent when not in edit mode" requirement.
- **Edit path:** a shell-level toggle (e.g. a pencil in `Page.Header` or the
  header bar) flips `editable`. Draft-then-save + dirty guards are lifted from
  the proven `ProfileEditor` (extract its Save/Discard/palette chrome into a
  reusable `useLayoutDraft(scope)` hook so Profile and every dashboard share it).
- **Rollout is opt-in-but-uniform:** a page adopts DnD by rendering its widgets
  through `DashboardCanvas` instead of a hand-built grid. Pages not yet migrated
  keep their current markup; there is no forced big-bang.

The transitional nuance: Overview currently renders charts as a bespoke
`space-y-6` stack of `<ChartCard>`s, NOT as grid tiles. Moving it onto
`DashboardCanvas` means expressing those charts as catalog widgets (tag them
`dashboardScopes: ["overview"]`). That is real work and belongs in the DnD phase,
not the shell phase — the shell/POM lands first with Overview still a static
stack inside `Page.Content`.

---

## 4. Unified dashboard-config store

**Reconciliation: the unified store already exists — `dashboard_layouts`.** Do
NOT build a new table. The plan is to *widen* the existing one from "public
profile only" to "any dashboard scope."

### 4.1 Schema (unchanged)

```sql
-- internal/db/migrations/00032_dashboard_layouts.sql (EXISTING)
dashboard_layouts(
  id UUID PK, owner TEXT→users ON DELETE CASCADE,
  scope TEXT, layout JSONB, updated_at TIMESTAMPTZ,
  UNIQUE(owner, scope)
)
```

**Scope key convention** (the one new thing — a naming scheme, not a migration):

| Page | scope key |
|---|---|
| Public profile | `public_profile` *(exists)* |
| Global Overview | `overview` |
| Per-space dashboard | `space:<id>` |
| Projects dashboard | `projects` |

`layout` JSONB envelope stays `{ "cols": 12, "widgets": GridLayoutItem[] }`.

### 4.2 Server change (small + safe)

Widen the allowlist in `internal/spaces/dashboard_layout.go`:

```go
// from:
var dashboardLayoutScopes = map[string]bool{"public_profile": true}
// to (prefix-aware for space:<id>):
func validScope(s string) bool {
  switch s {
  case "public_profile", "overview", "projects": return true
  }
  return strings.HasPrefix(s, "space:")   // + verify the space is owned by caller
}
```

Keep the 4 KiB body cap. `layout` stays opaque JSONB (the FE renderer already
drops unknown `kind`s, so no server-side widget validation needed). Optionally
add the deferred `updated_at` optimistic-concurrency token the envelope was
shaped for — nice-to-have, not required for v1.

### 4.3 Client hook (one shared API surface)

Generalize the existing per-scope adapter into a single hook every dashboard
page uses (sketch):

```tsx
// web/src/hooks/useDashboardConfig.ts  (SKETCH)
export function useDashboardConfig(scope: string) {
  const qc = useQueryClient();
  const query = useQuery({
    queryKey: qk.dashboardLayout(scope),
    queryFn: () => api.getDashboardLayout(scope).catch(() => null),
  });
  const save = useMutation({
    mutationFn: (widgets: GridLayoutItem[]) => api.putDashboardLayout(scope, { cols: 12, widgets }),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.dashboardLayout(scope) }),
  });
  const reset = useMutation({
    mutationFn: () => api.deleteDashboardLayout(scope),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.dashboardLayout(scope) }),
  });
  return { layout: query.data?.layout?.widgets ?? null, isLoading: query.isLoading, save, reset };
}
```

`qk.dashboardLayout(scope)`, `api.getDashboardLayout/putDashboardLayout/
deleteDashboardLayout` all already exist (used by `dbStorageAdapter` +
`ProfileEditor`). So ALL dashboard pages persist config through ONE table, ONE
endpoint triple, ONE hook — with `dbStorageAdapter` retained as the
`StorageAdapter` bridge into the grid primitive.

---

## 5. Admin CX

### 5.1 Current

`/app/admin` (`AdminPage.tsx`) = a flat 4-tab strip → `Users`, `Labels`,
`Backfill`, `Logs`, each a lazy child route. Observations:
- **`Labels` (AdminTab.tsx) is 63 KB** — by far the heaviest surface, mixing
  label CRUD, condition/rule building, and image generation. It dwarfs the
  others and is really several tools.
- `Backfill` (36 KB) is an operational job runner; `Logs` is an operational feed
  (also reachable via the legacy `/app/logs` redirect). Both are "watch the
  system do work" surfaces.
- `Users` is user administration.
- Flat peers hide the natural split: **configuration** (define labels/rules) vs
  **operations** (run backfills, watch logs, manage users).

### 5.2 Proposed

```
Admin
├─ Operations            ← "run + observe"
│  ├─ Users              (user admin, roles)
│  ├─ Backfill           (job runner)
│  └─ Logs               (live feed)
└─ Configuration         ← "define"
   └─ Labels & Rules     (split the 63 KB AdminTab into: Labels · Conditions · Images sub-tabs)
```

Concretely:
- Keep the NavLink child-route pattern (deep-linkable URLs) but introduce a
  **two-group tab strip** (or a left sub-nav within Admin) separating
  Operations from Configuration.
- **Decompose `AdminTab.tsx`** (Labels) into `Labels`, `Conditions`
  (`ConditionBuilder/` already isolated), and `Images` sub-tabs so no single
  route is 63 KB and each is independently lazy-loaded.
- **Move genuinely user-scoped controls OUT of Admin.** Audit each Admin control:
  anything that configures the *current user's* experience (not the deployment)
  belongs in Settings, not Admin. (Backfill/Logs/Users are correctly
  deployment-level; the Labels surface is where per-user vs global blurs — split
  accordingly.)
- Once dashboards persist through §4, an Admin "Ops dashboard" could itself be a
  `DashboardCanvas` scope (`overview`-style tiles for system health) — natural
  future, not required.

**Before → after:** flat `[Users][Labels][Backfill][Logs]` →
`Operations[Users · Backfill · Logs]` + `Configuration[Labels · Conditions ·
Images]`.

---

## 6. Phased migration plan

Each phase is independently shippable and push-safe (no phase leaves the app
broken; every phase is a self-contained PR).

**Phase A — Shell + POM primitives, ONE pilot page.** Land `Page.tsx` +
`AppShellNoScroll.tsx` (done, unwired). Fold the no-scroll grid into
`AppShell.tsx` (keep Sidebar/HeaderBar/dialogs). Migrate **one** pilot page —
recommend **Leaderboards** (small, no widgets panel, no bespoke scroll) — onto
`<Page>`. Verify no scroll regression on short AND tall content, mobile
included. *Smallest shippable slice.*

**Phase B — Migrate remaining pages onto `<Page>`.** Overview (+ SpaceView via
it), Projects, Heartbeats, Wellness, Import, Settings, Admin, Logs, Changelog.
One PR per 1–2 pages. Pure composition swap; no behavior change. Unify the two
tab-strip implementations (Admin NavLink + Settings `?tab=`) into a shared
`Page`-level tab primitive along the way.

**Phase C — Transparent DnD shim.** Add `DashboardCanvas` + extract
`useLayoutDraft(scope)` from `ProfileEditor`. Tag Overview charts as
`dashboardScopes:["overview"]` catalog widgets. Ship an "edit layout" toggle on
Overview first (behind the existing feature-flag pattern), then Projects/Spaces.

**Phase D — Config unification.** Widen the server scope allowlist
(§4.2); add `useDashboardConfig`; point every `DashboardCanvas` at it. Backfill
`space:<id>` + `overview` scopes. Refactor `ProfileEditor` to consume the shared
hook (public-profile behavior unchanged).

**Phase E — Admin CX.** Regroup into Operations/Configuration; decompose the
63 KB `AdminTab`; relocate any user-scoped controls to Settings.

Dependency order: A→B (POM must exist first). C depends on B for Overview.
D depends on C (canvas consumers exist). E is independent of C/D and can slot
after B.

---

## 7. Risks

1. **Scroll regressions (highest).** The shell rewrite touches the one layout
   every page depends on. `min-h-0` omissions silently break inner scrolling
   (whole shell scrolls instead). Pages with their own `h-screen`/`h-[60vh]`
   (`PageFallback`, `RootRedirect`, `ProfileEditor`'s `h-[60vh]` spinner, the
   `fixed` Save chrome) must be reconciled. *Mitigation:* pilot page + explicit
   short/tall/mobile QA in Phase A before Phase B; keep `AppShell` and
   `AppShellNoScroll` side-by-side until the pilot proves out.
2. **Data-router shell coupling.** `main.tsx` mounts a single `path:"*"`
   catch-all lazy-loading `AppRoutes`; `useBlocker`/`beforeunload` in
   `ProfileEditor` depend on the data router. Folding the shell must not disturb
   the `RootLayout`→`AppRoutes` split or the lazy chunk boundaries (stale-chunk
   recovery in `chunkReload` + `RouteErrorBoundary`).
3. **Mobile/responsive.** Sidebar is `hidden md:flex` today (no mobile drawer).
   `h-dvh` + grid must degrade on small screens; the `Page.Aside` rail is
   `hidden lg:block`. Verify iOS Safari dynamic-viewport behavior and that
   `Page.Content` is the scroller (not the body) on touch.
4. **DnD scope creep.** Overview's charts aren't catalog widgets yet; converting
   them (Phase C) is more than a wrapper swap. Keep it out of the shell phase.
5. **Server scope allowlist.** Widening `dashboardLayoutScopes` must verify
   `space:<id>` ownership (don't let a user PUT a layout for a space they don't
   own). Keep the 4 KiB cap.

---

## 8. Prototype files added

- `web/src/layout/Page.tsx` — `Page` + `Page.Header/Body/Content/Aside`; wraps
  catalyst-ui `<PageToolbar>`; enforces the single-scroll-region invariant.
- `web/src/layout/AppShellNoScroll.tsx` — slot-based `100dvh` CSS-grid shell.

Both are **unwired** (no route imports them). `cd web && yarn typecheck` passes.
Sketches in §3/§4 (`DashboardCanvas`, `useDashboardConfig`, `useLayoutDraft`)
are intentionally NOT shipped as files — they belong to Phases C/D and pull in
domain deps this spike deliberately leaves untouched.

---

## 9. Recommended first slice

**Phase A with Leaderboards as pilot.** It has a `<PageToolbar>` + a single
range picker + a plain card list — no widgets panel, no bespoke scroll-into-view,
no tabs. Migrating it exercises the full `Page`/shell path with the least
surface area, so a scroll regression shows up immediately and cheaply. Ship:
(1) no-scroll grid folded into `AppShell`, (2) Leaderboards on `<Page>`, (3)
short/tall/mobile QA. Everything else follows once that holds.
