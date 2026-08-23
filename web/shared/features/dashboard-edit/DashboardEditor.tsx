// DashboardEditor (boom-lzr, Phase 4) — the reusable edit shell, exposed as a
// hook so a page (OverviewDashboard) can slot its three pieces into the POM
// shell independently: `chrome` → Page.Header, `sidebar` → Page.Body aside,
// `content` → Page.Content.
//
// It owns the local edit store (seeded from OVERVIEW_DEFAULT_LAYOUT) and bridges
// it to the isolated grid primitive via a store-backed StorageAdapter. No DB in
// Phase 4 — persistence is Phase 6.
//
// Why a hook and not a component: the mode toggle, the aside, and the grid land
// in three DIFFERENT slots of <Page>. A single component would have to render
// all three in one subtree; a hook lets the page place each node where the
// shell wants it while a single store instance backs them all.
//
import { useMemo, useRef, useState, type ReactNode } from "react";
import {
  DraggableGridLayout,
  type StorageAdapter,
  type WidgetInstance,
} from "@shared/lib/grid";
import {
  catalogForDashboard,
  type DashboardScope,
  type WidgetCatalogEntry,
} from "@shared/features/widgets/catalog";
import { OverviewWidgetRenderer } from "@shared/features/widgets/renderers/OverviewWidgetRenderer";
import { OVERVIEW_DEFAULT_LAYOUT } from "@shared/features/overview/overviewDefaults";
import {
  add,
  moveResize,
  useDashboardEditStore,
} from "./useDashboardEditStore";
import {
  DashboardEditChrome,
  type DashboardEditMode,
} from "./DashboardEditChrome";
import { DashboardEditSidebar } from "./DashboardEditSidebar";

export interface UseDashboardEditorResult {
  mode: DashboardEditMode;
  isEdit: boolean;
  /** Page.Header chrome: Edit/Preview toggle + undo/redo + dirty indicator. */
  chrome: ReactNode;
  /** Page.Body aside content (edit mode only; null in preview). */
  sidebar: ReactNode;
  /** Page.Content: the draggable widget grid (editable in edit mode). */
  content: ReactNode;
}

/**
 * Wire the edit store + grid + chrome + sidebar for a dashboard `scope`.
 * Phase 4 is Overview-only (`"overview"`); the seed is OVERVIEW_DEFAULT_LAYOUT
 * and the catalog is `catalogForDashboard("overview")`. Per-space scoping of the
 * LAYOUT is Phase 7 — the widgets still self-fetch scoped by the existing
 * `space` prop through OverviewDataContext.
 */
export function useDashboardEditor(
  scope: DashboardScope = "overview",
): UseDashboardEditorResult {
  const [mode, setMode] = useState<DashboardEditMode>("preview");
  const isEdit = mode === "edit";

  const store = useDashboardEditStore(OVERVIEW_DEFAULT_LAYOUT);
  const {
    state,
    dispatch,
    undo,
    redo,
    canUndo,
    canRedo,
    isDirty,
    selectedKey,
    setSelectedKey,
    structuralRev,
  } = store;

  // Catalog entries for this dashboard scope + the "add" palette (catalog −
  // in-layout).
  const entries = useMemo<WidgetCatalogEntry[]>(
    () => catalogForDashboard(scope),
    [scope],
  );
  const inLayout = useMemo(
    () => new Set(state.layout.map((w) => w.i)),
    [state.layout],
  );
  const paletteEntries = useMemo(
    () => entries.filter((e) => !inLayout.has(e.kind)),
    [entries, inLayout],
  );

  // WidgetInstances: each renders the self-fetching OverviewWidgetRenderer.
  const instances = useMemo<WidgetInstance[]>(
    () =>
      entries.map((e) => ({
        key: e.kind,
        displayName: e.title,
        defaultLayout: e.defaultLayout,
        views: e.views,
        defaultView: e.defaultView,
        render: (ctx) => (
          <OverviewWidgetRenderer
            kind={e.kind}
            view={ctx.view}
            config={ctx.config}
          />
        ),
      })),
    [entries],
  );

  // Store-backed StorageAdapter. Memoized on `scope` (a stable identity) so the
  // grid's mount effect doesn't re-run on every store update; a live `layoutRef`
  // feeds the grid the current layout on (re)mount.
  const layoutRef = useRef(state.layout);
  layoutRef.current = state.layout;
  const storage = useMemo<StorageAdapter>(
    () => ({
      async load() {
        return layoutRef.current;
      },
      async save(layout) {
        dispatch(moveResize(layout));
      },
    }),
    // dispatch is stable (useReducer) and layoutRef always reflects the current
    // layout, so the adapter identity is stable for the editor's lifetime — the
    // grid's mount effect must NOT re-run on every store update. (Space swaps
    // remount OverviewDashboard via its `key`, so scope never changes in place.)
    [dispatch],
  );

  const handleAdd = (kind: string) => {
    const entry = entries.find((e) => e.kind === kind);
    if (entry) dispatch(add(kind, entry));
  };

  const chrome = (
    <DashboardEditChrome
      mode={mode}
      onModeChange={setMode}
      canUndo={canUndo}
      canRedo={canRedo}
      onUndo={undo}
      onRedo={redo}
      isDirty={isDirty}
    />
  );

  const sidebar = isEdit ? (
    <DashboardEditSidebar
      paletteEntries={paletteEntries}
      selectedKey={selectedKey}
      onAdd={handleAdd}
    />
  ) : null;

  const content = (
    // Key on structuralRev so add/remove/undo/redo re-seed the grid from the
    // store, while a live drag (moveResize) never bumps it and so never
    // remounts mid-gesture.
    <DraggableGridLayout
      key={structuralRev}
      instances={instances}
      storage={storage}
      editable={isEdit}
      cols={12}
      seedLayout={state.layout}
      selectedKey={selectedKey}
      onSelectTile={isEdit ? setSelectedKey : undefined}
    />
  );

  return { mode, isEdit, chrome, sidebar, content };
}
