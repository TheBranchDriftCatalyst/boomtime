// useDashboardEditStore.test.ts (boom-lzr, Phase 4) — the edit store's history
// contract: undo/redo pointer math, moveResize drag-coalescing, select being a
// non-history / non-dirtying present mutation, the bounded past stack, and
// isDirty vs markSaved.
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { GridLayoutItem } from "@shared/lib/grid";
import {
  HISTORY_CAP,
  add,
  editReducer,
  initEditState,
  moveResize,
  remove,
  select,
  setView,
  useDashboardEditStore,
} from "./useDashboardEditStore";

const seed: GridLayoutItem[] = [
  { i: "a", x: 0, y: 0, w: 6, h: 3 },
  { i: "b", x: 6, y: 0, w: 6, h: 3 },
];

// A distinct layout (different geometry) for each move, so the no-op guard
// never collapses them.
function move(h: number): GridLayoutItem[] {
  return [
    { i: "a", x: 0, y: 0, w: 6, h },
    { i: "b", x: 6, y: 0, w: 6, h: 3 },
  ];
}

describe("editReducer (pure)", () => {
  it("coalesces consecutive moveResize within the idle window into ONE entry", () => {
    let s = initEditState(seed);
    s = editReducer(s, moveResize(move(4), 0));
    s = editReducer(s, moveResize(move(5), 100));
    s = editReducer(s, moveResize(move(6), 200));
    // Present advanced to the last layout, but only ONE snapshot was pushed.
    expect(s.present.layout).toEqual(move(6));
    expect(s.past).toHaveLength(1);
    expect(s.past[0].layout).toEqual(seed); // the pre-gesture snapshot
  });

  it("starts a NEW history entry when the idle window elapses between moves", () => {
    let s = initEditState(seed);
    s = editReducer(s, moveResize(move(4), 0));
    s = editReducer(s, moveResize(move(5), 5000)); // > COALESCE_WINDOW_MS gap
    expect(s.past).toHaveLength(2);
  });

  it("no-ops an identical-layout moveResize (RGL on-mount reflow guard)", () => {
    let s = initEditState(seed);
    s = editReducer(s, moveResize(seed, 0));
    expect(s.past).toHaveLength(0); // nothing pushed
    expect(s.present.layout).toEqual(seed);
  });

  it("select mutates present WITHOUT pushing history or dirtying", () => {
    let s = initEditState(seed);
    s = editReducer(s, select("a"));
    expect(s.present.selectedKey).toBe("a");
    expect(s.past).toHaveLength(0);
    expect(s.saved).toEqual(seed); // baseline untouched → still clean
  });

  it("bounds the past stack at HISTORY_CAP", () => {
    let s = initEditState(seed);
    for (let k = 0; k < HISTORY_CAP + 10; k++) {
      // Distinct layout + far-apart timestamps so every dispatch is its own entry.
      s = editReducer(s, moveResize(move(k + 4), k * 5000));
    }
    expect(s.past).toHaveLength(HISTORY_CAP);
  });

  it("add appends at maxY and bumps the structural revision", () => {
    let s = initEditState(seed);
    const rev0 = s.structuralRev;
    s = editReducer(s, add("c", { defaultLayout: { w: 4, h: 2 }, defaultView: "pie" }));
    expect(s.present.layout).toHaveLength(3);
    const added = s.present.layout[2];
    expect(added).toMatchObject({ i: "c", y: 3, w: 4, h: 2, view: "pie" });
    expect(s.structuralRev).toBe(rev0 + 1);
    expect(s.past).toHaveLength(1);
  });

  it("remove drops the tile and clears a matching selection", () => {
    let s = initEditState(seed);
    s = editReducer(s, select("a"));
    s = editReducer(s, remove("a"));
    expect(s.present.layout.map((w) => w.i)).toEqual(["b"]);
    expect(s.present.selectedKey).toBeNull();
  });
});

describe("useDashboardEditStore (hook)", () => {
  it("undo/redo pointer math walks the coalesced gesture as one step", () => {
    const { result } = renderHook(() => useDashboardEditStore(seed));

    act(() => {
      result.current.dispatch(moveResize(move(4), 0));
      result.current.dispatch(moveResize(move(5), 100));
      result.current.dispatch(moveResize(move(6), 200));
    });
    expect(result.current.state.layout).toEqual(move(6));
    expect(result.current.canUndo).toBe(true);
    expect(result.current.canRedo).toBe(false);
    expect(result.current.isDirty).toBe(true);

    act(() => result.current.undo());
    // One undo returns to the pre-gesture (seed) state — proves single entry.
    expect(result.current.state.layout).toEqual(seed);
    expect(result.current.canUndo).toBe(false);
    expect(result.current.canRedo).toBe(true);
    expect(result.current.isDirty).toBe(false); // back at the saved baseline

    act(() => result.current.redo());
    expect(result.current.state.layout).toEqual(move(6));
    expect(result.current.canRedo).toBe(false);
    expect(result.current.isDirty).toBe(true);
  });

  it("select does not create an undo step or set dirty", () => {
    const { result } = renderHook(() => useDashboardEditStore(seed));
    act(() => result.current.setSelectedKey("b"));
    expect(result.current.selectedKey).toBe("b");
    expect(result.current.canUndo).toBe(false);
    expect(result.current.isDirty).toBe(false);
  });

  it("markSaved rebaselines dirty; an undo away from it dirties again", () => {
    const { result } = renderHook(() => useDashboardEditStore(seed));

    act(() => result.current.dispatch(add("c", { defaultLayout: { w: 4, h: 2 } })));
    expect(result.current.isDirty).toBe(true);

    act(() => result.current.markSaved());
    expect(result.current.isDirty).toBe(false); // new baseline == current

    act(() => result.current.undo());
    expect(result.current.state.layout).toEqual(seed);
    expect(result.current.isDirty).toBe(true); // present ≠ saved baseline again
  });

  it("setView records a history entry and applies the view", () => {
    const { result } = renderHook(() => useDashboardEditStore(seed));
    act(() => result.current.dispatch(setView("a", "bar")));
    expect(result.current.state.layout.find((w) => w.i === "a")?.view).toBe("bar");
    expect(result.current.canUndo).toBe(true);
    act(() => result.current.undo());
    expect(result.current.state.layout.find((w) => w.i === "a")?.view).toBeUndefined();
  });
});
