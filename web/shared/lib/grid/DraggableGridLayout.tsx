// DraggableGridLayout — the isolated grid primitive's public entry
// component. Scope-agnostic: takes instances + layout + storage, knows
// nothing about boomtime's public profile / overview / etc.
//
// Mirrors @hakboard-dashboard/src/components/Grid.tsx for pattern parity;
// key divergences documented at the composition site (boomtime's
// PublicDashboard.tsx).
//
// The `@types/react-grid-layout` package on npm is stale relative to the
// runtime v2.2.3 API (useContainerWidth, dragConfig are missing). We
// pull the runtime helpers directly and cast to `any` at the module
// boundary — a small, honest concession noted here so a future update to
// the types package can drop the casts.
import { useEffect, useMemo, useState } from "react";
// eslint-disable-next-line @typescript-eslint/no-explicit-any
import * as RGL from "react-grid-layout";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import "./grid.css";

import { WidgetHost } from "./WidgetHost";
import { applyPositions, buildDefaultLayout } from "./layout-evolution";
import type { GridLayoutItem, StorageAdapter, WidgetInstance } from "./types";

// Direct runtime references. `Responsive` is well-typed; `useContainerWidth`
// isn't in the type package but exists in runtime v2.2.3 (verified in
// react-grid-layout/dist/index.js). Cast the module through `any` to
// reach it without a type-only import that would error.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const rglAny = RGL as any;
const Responsive = rglAny.Responsive as React.ComponentType<Record<string, unknown>>;
const useContainerWidth = rglAny.useContainerWidth as () => {
  width: number;
  containerRef: React.RefObject<HTMLElement>;
  mounted: boolean;
};

// RGL's Layout item shape (verbatim from @types/react-grid-layout).
type Layout = {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
  isDraggable?: boolean;
  isResizable?: boolean;
  static?: boolean;
};

// Collapse a multi-column layout into a single-column vertical stack for phone
// breakpoints (boom-k26n.2). A 12-col magazine layout is illegible at 375px (a
// w=3 tile is ~28px), so we preserve reading order (row-major), give every tile
// the full mobile width, and re-flow y so nothing overlaps. Pure + module-level
// so it's trivially testable. Applied READ-ONLY only — see the wiring below.
export function stackForMobile(base: Layout[], mobileCols: number): Layout[] {
  const ordered = [...base].sort((a, b) => a.y - b.y || a.x - b.x);
  let y = 0;
  return ordered.map((w) => {
    const item = { ...w, x: 0, y, w: mobileCols };
    y += w.h;
    return item;
  });
}

export interface DraggableGridLayoutProps {
  /** Widget instances available for this dashboard (the full catalog for the
   * scope). The rendered set is the intersection of a saved layout with this
   * list — instances NOT in the saved layout are available to add (via an
   * editor palette) but are not auto-injected, so intentional removals stick. */
  instances: WidgetInstance[];
  /** Persistence adapter. `null` return from adapter.load() = fall back to
   * defaults. save() fires on every layout change (drag/resize/view). */
  storage: StorageAdapter;
  /** Read-only (false) or edit-mode (true). Read-only tiles have static
   * placement and no remove-X, but ChartToggle interactions still work. */
  editable: boolean;
  /** Total column count. 12 by default; the design consumer decides. */
  cols?: number;
  /** Row height in pixels (react-grid-layout units). 48 default. */
  rowHeight?: number;
  /** If provided, INITIAL layout that seeds when storage returns null. */
  seedLayout?: GridLayoutItem[];
  /** Edit-mode tile selection (boom-lzr). When set, the matching tile gets
   * `data-selected` for styling and the config sidebar targets it. */
  selectedKey?: string | null;
  /** Fired when a tile is selected (its key) or the empty grid is clicked
   * (null, to clear). Only wired in edit mode. */
  onSelectTile?: (key: string | null) => void;
}

export function DraggableGridLayout({
  instances,
  storage,
  editable,
  cols = 12,
  rowHeight = 48,
  seedLayout,
  selectedKey,
  onSelectTile,
}: DraggableGridLayoutProps) {
  const [layout, setLayout] = useState<GridLayoutItem[]>(() =>
    seedLayout ?? buildDefaultLayout(instances, cols),
  );
  const { width, containerRef, mounted } = useContainerWidth();

  // Load persisted layout once on mount. A saved layout is AUTHORITATIVE:
  // we render exactly what the user saved (minus keys whose widget no longer
  // exists in the catalog), and never auto-append catalog widgets they left
  // out. Auto-appending defaults here made intentional removals impossible —
  // a removed widget is indistinguishable from a brand-new one, so it came
  // straight back on the next load ("save re-adds everything"). New catalog
  // widgets stay discoverable via the editor palette (catalog − in-layout).
  useEffect(() => {
    let alive = true;
    (async () => {
      const persisted = await storage.load();
      if (!alive) return;
      const defaults = buildDefaultLayout(instances, cols);
      const initial =
        persisted && persisted.length > 0
          ? persisted.filter((w) => instances.some((i) => i.key === w.i))
          : (seedLayout ?? defaults);
      setLayout(initial);
    })();
    return () => {
      alive = false;
    };
    // Re-run when storage identity changes (scope swap).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [storage]);

  // "Hide but keep placement" (gaka-lzr Phase 5): a hidden tile stays in
  // `layout` (geometry intact, still round-trips through save/persistence)
  // but is dropped from the RENDER entirely in preview/view mode — that's
  // the whole point of a visibility toggle. In edit mode it stays visible
  // (dimmed — see WidgetHost's `hidden` prop) so the operator can find it
  // again to un-hide it via the CONFIGURE panel.
  const renderableLayout = useMemo(
    () =>
      layout.filter(
        (w) => instances.some((i) => i.key === w.i) && (editable || !w.hidden),
      ),
    [layout, instances, editable],
  );

  const rglLayout = useMemo<Layout[]>(
    () =>
      renderableLayout.map((w) => ({
        i: w.i,
        x: w.x,
        y: w.y,
        w: w.w,
        h: w.h,
        isDraggable: editable,
        isResizable: editable,
        static: !editable,
      })),
    [renderableLayout, editable],
  );

  const handleLayoutChange = (next: readonly Layout[]) => {
    // Read-only grid: RGL still fires onLayoutChange on first mount (its
    // internal breakpoint pick can reflow items). Don't save those — they
    // would trample the seed layout on a page load. We also honor the
    // `static: true` per-item flag so drag never fires here in read-only.
    if (!editable) return;
    // Preserve per-item view/hidden/config metadata across RGL's geometry-only
    // shape (boom-lzr). Extracted to a pure helper so the contract is tested.
    const merged = applyPositions(layout, next);
    setLayout(merged);
    void storage.save(merged);
  };

  const handleViewChange = (key: string, nextView: string) => {
    const merged = layout.map((w) => (w.i === key ? { ...w, view: nextView } : w));
    setLayout(merged);
    void storage.save(merged);
  };

  const handleRemove = (key: string) => {
    const merged = layout.filter((w) => w.i !== key);
    setLayout(merged);
    void storage.save(merged);
  };

  // Phone-breakpoint collapse (boom-k26n.2). In READ mode we swap in a
  // single-column stacked layout (and 1 col) at xs/xxs so the public /p/:slug
  // dashboard is legible on a phone. EDIT mode is deliberately left on the
  // full grid at every breakpoint: the editor is desktop-only, and swapping
  // the layout under RGL there would let a mobile-breakpoint onLayoutChange
  // persist the stacked positions over the user's real desktop layout
  // (handleLayoutChange only early-returns in read mode).
  const mobileStack = useMemo(() => stackForMobile(rglLayout, 1), [rglLayout]);
  const layoutsByBreakpoint = editable
    ? { lg: rglLayout, md: rglLayout, sm: rglLayout, xs: rglLayout, xxs: rglLayout }
    : { lg: rglLayout, md: rglLayout, sm: rglLayout, xs: mobileStack, xxs: mobileStack };
  const colsByBreakpoint = editable
    ? { lg: cols, md: cols, sm: cols, xs: cols, xxs: cols }
    : { lg: cols, md: cols, sm: cols, xs: 1, xxs: 1 };

  return (
    <div
      ref={containerRef as React.RefObject<HTMLDivElement>}
      style={{ width: "100%" }}
      className={`catalyst-grid${editable ? " catalyst-grid--editing" : ""}`}
      data-testid="catalyst-grid"
      // Clicking empty grid space (not a tile) clears the selection (boom-lzr).
      onClick={
        editable && onSelectTile
          ? (e) => {
              if (e.target === e.currentTarget) onSelectTile(null);
            }
          : undefined
      }
    >
      {mounted && width > 0 && (
        <Responsive
          width={width}
          containerPadding={[0, 0]}
          margin={[12, 12]}
          onLayoutChange={handleLayoutChange}
          dragConfig={{ cancel: "button, a, .no-drag" }}
          rowHeight={rowHeight}
          layouts={layoutsByBreakpoint}
          breakpoints={{ lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }}
          cols={colsByBreakpoint}
          compactType={editable ? "vertical" : null}
          isDroppable={false}
        >
          {renderableLayout.map((w, idx) => {
              const inst = instances.find((i) => i.key === w.i)!;
              return (
                <WidgetHost
                  key={w.i}
                  tileIndex={idx}
                  instance={inst}
                  view={w.view ?? undefined}
                  config={w.config}
                  editable={editable}
                  selected={selectedKey === w.i}
                  hidden={w.hidden}
                  onSelect={onSelectTile ? () => onSelectTile(w.i) : undefined}
                  onViewChange={(v) => handleViewChange(w.i, v)}
                  onRemove={() => handleRemove(w.i)}
                />
              );
            })}
        </Responsive>
      )}
    </div>
  );
}
