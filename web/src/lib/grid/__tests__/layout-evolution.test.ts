// layout-evolution tests — additive merge is the "user layout doesn't
// break when we add a new widget kind" contract. Trivial function; the
// tests exist as guardrails for a subtle regression (dropping additions,
// or duplicating a key on merge).
import { describe, expect, it } from "vitest";
import { buildDefaultLayout, mergeLayouts } from "../layout-evolution";
import type { WidgetInstance } from "../types";

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
});
