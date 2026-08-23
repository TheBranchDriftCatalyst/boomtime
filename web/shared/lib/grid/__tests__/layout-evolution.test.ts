// layout-evolution tests — additive merge is the "user layout doesn't
// break when we add a new widget kind" contract. Trivial function; the
// tests exist as guardrails for a subtle regression (dropping additions,
// or duplicating a key on merge).
import { describe, expect, it } from "vitest";
import { applyPositions, buildDefaultLayout, mergeLayouts } from "../layout-evolution";
import type { GridLayoutItem, WidgetInstance } from "../types";

const inst = (key: string, w = 6, h = 3): WidgetInstance => ({
  key,
  render: () => null,
  defaultLayout: { w, h },
});

describe("buildDefaultLayout", () => {
  it("packs widgets left-to-right and wraps at cols", () => {
    const layout = buildDefaultLayout([inst("a", 6, 3), inst("b", 6, 3), inst("c", 4, 2)], 12);
    expect(layout).toEqual([
      { i: "a", x: 0, y: 0, w: 6, h: 3, view: null },
      { i: "b", x: 6, y: 0, w: 6, h: 3, view: null },
      { i: "c", x: 0, y: 3, w: 4, h: 2, view: null },
    ]);
  });

  it("uses 6x3 as fallback for widgets without defaultLayout", () => {
    const bare: WidgetInstance = { key: "z", render: () => null };
    const layout = buildDefaultLayout([bare], 12);
    expect(layout).toEqual([{ i: "z", x: 0, y: 0, w: 6, h: 3, view: null }]);
  });
});

describe("mergeLayouts", () => {
  it("preserves persisted items and appends new defaults", () => {
    const persisted = [{ i: "a", x: 3, y: 5, w: 6, h: 3, view: "bar" as string | null }];
    const defaults = [
      { i: "a", x: 0, y: 0, w: 6, h: 3, view: null as string | null },
      { i: "b", x: 6, y: 0, w: 6, h: 3, view: null as string | null },
    ];
    const merged = mergeLayouts(persisted, defaults);
    expect(merged).toHaveLength(2);
    // Persisted "a" wins (view=bar, position=3/5).
    expect(merged[0]).toEqual({ i: "a", x: 3, y: 5, w: 6, h: 3, view: "bar" });
    // New "b" is appended from defaults.
    expect(merged[1]).toEqual({ i: "b", x: 6, y: 0, w: 6, h: 3, view: null });
  });

  it("returns persisted only when defaults is a subset", () => {
    const persisted = [
      { i: "a", x: 0, y: 0, w: 6, h: 3, view: null as string | null },
      { i: "b", x: 6, y: 0, w: 6, h: 3, view: null as string | null },
    ];
    const defaults = [{ i: "a", x: 0, y: 0, w: 6, h: 3, view: null as string | null }];
    const merged = mergeLayouts(persisted, defaults);
    expect(merged).toEqual(persisted);
  });

  it("preserves a config blob across merge (config rides on the whole item)", () => {
    const persisted: GridLayoutItem[] = [
      { i: "a", x: 3, y: 5, w: 6, h: 3, view: "bar", config: { topN: 8, title: "Mine" } },
    ];
    const defaults: GridLayoutItem[] = [{ i: "b", x: 6, y: 0, w: 6, h: 3, view: null }];
    const merged = mergeLayouts(persisted, defaults);
    expect(merged[0].config).toEqual({ topN: 8, title: "Mine" });
  });
});

// boom-lzr: applyPositions is the drag/resize merge — RGL's onLayoutChange
// only carries geometry, so this MUST preserve view/hidden/config or every
// drag would wipe a widget's chart-toggle view + its config blob.
describe("applyPositions", () => {
  it("merges new geometry while preserving view/hidden/config metadata", () => {
    const prev: GridLayoutItem[] = [
      { i: "a", x: 0, y: 0, w: 6, h: 3, view: "pie", hidden: false, config: { topN: 5 } },
      { i: "b", x: 6, y: 0, w: 6, h: 3, view: null },
    ];
    // RGL reports a drag: "a" moved to x=3,y=2 and grew to w=8; geometry only.
    const next = [
      { i: "a", x: 3, y: 2, w: 8, h: 4 },
      { i: "b", x: 0, y: 6, w: 6, h: 3 },
    ];
    const merged = applyPositions(prev, next);
    expect(merged[0]).toEqual({
      i: "a", x: 3, y: 2, w: 8, h: 4, view: "pie", hidden: false, config: { topN: 5 },
    });
    expect(merged[1]).toEqual({
      i: "b", x: 0, y: 6, w: 6, h: 3, view: null, hidden: undefined, config: undefined,
    });
  });

  it("defaults metadata for an item with no prior entry", () => {
    const merged = applyPositions([], [{ i: "new", x: 0, y: 0, w: 4, h: 2 }]);
    expect(merged[0]).toEqual({
      i: "new", x: 0, y: 0, w: 4, h: 2, view: null, hidden: undefined, config: undefined,
    });
  });
});
