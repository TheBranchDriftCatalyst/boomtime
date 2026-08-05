// Public types for the isolated grid primitive (gaka-6qg extraction target).
//
// This folder is a self-contained npm-package-in-waiting: nothing here
// imports from boomtime-domain code. See grid.css for the CSS-var contract
// consumers use to skin tiles without leaking design tokens into the
// primitive.

import type { ReactNode } from "react";

/** One layout entry — the position + optional per-widget config carried
 * across sessions. `i` is a caller-defined stable id (widget kind in
 * boomtime). `view` and `hidden` are optional extension slots for chart-
 * toggle state and edit-mode "hide but keep placement" state. `config` is an
 * opaque per-widget config blob (gaka-lzr) — the primitive never inspects it,
 * only round-trips it; the consumer (boomtime's widget config schema) owns its
 * shape. Kept as `Record<string, unknown>` so the primitive stays domain-free. */
export interface GridLayoutItem {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
  view?: string | null;
  hidden?: boolean;
  config?: Record<string, unknown>;
}

/** A widget instance to render at a layout entry. The primitive doesn't
 * know what the widget IS — it only knows how to render one cell and hand
 * it a size context. `render` receives the tile's measured size and the
 * current per-widget view id. */
export interface WidgetInstance {
  key: string;
  displayName?: string;
  render: (ctx: {
    view?: string;
    width: number;
    height: number;
    /** The layout entry's opaque per-widget config blob (gaka-lzr), if any. */
    config?: Record<string, unknown>;
  }) => ReactNode;
  defaultLayout?: { w: number; h: number };
  /** When set, ChartToggle in the tile header will offer these view options. */
  views?: { id: string; label: string; icon?: string }[];
  defaultView?: string;
}

/** Persistence adapter contract. The primitive doesn't know or care where
 * layouts are stored — pass an adapter. `null` from load = fall back to
 * defaults. */
export interface StorageAdapter {
  load: () => Promise<GridLayoutItem[] | null>;
  save: (layout: GridLayoutItem[]) => Promise<void>;
}
