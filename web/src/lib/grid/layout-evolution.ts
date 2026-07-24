// layout-evolution — additive merge helpers for the isolated grid primitive.
// Mirrors hakboard's evolution merge; layouts drift as widget catalogs grow
// so an out-of-date persisted layout must not lose new kinds.
import type { GridLayoutItem, WidgetInstance } from "./types";

/** Build a default layout from a list of widget instances. Packs them
 * left-to-right at 6-col-per-row width by default, respecting each
 * instance's `defaultLayout` size. `cols` is the total column width. */
export function buildDefaultLayout(
  instances: WidgetInstance[],
  cols = 12,
): GridLayoutItem[] {
  let x = 0;
  let y = 0;
  let rowMaxH = 0;
  const out: GridLayoutItem[] = [];
  for (const inst of instances) {
    const w = inst.defaultLayout?.w ?? 6;
    const h = inst.defaultLayout?.h ?? 3;
    if (x + w > cols) {
      x = 0;
      y += rowMaxH;
      rowMaxH = 0;
    }
    out.push({ i: inst.key, x, y, w, h, view: inst.defaultView ?? null });
    x += w;
    if (h > rowMaxH) rowMaxH = h;
  }
  return out;
}

/** Additive merge: persisted positions win for items present in both;
 * items present only in `defaults` are appended (new widgets get slotted
 * in with sane placements). Order preserved: persisted first, then
 * additions in `defaults` order. */
export function mergeLayouts(
  persisted: GridLayoutItem[],
  defaults: GridLayoutItem[],
): GridLayoutItem[] {
  const persistedKeys = new Set(persisted.map((w) => w.i));
  const additions = defaults.filter((w) => !persistedKeys.has(w.i));
  return [...persisted, ...additions];
}
