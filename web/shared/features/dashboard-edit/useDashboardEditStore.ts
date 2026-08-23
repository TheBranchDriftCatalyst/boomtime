// useDashboardEditStore (boom-lzr, Phase 4) — the local edit store for the
// in-app dashboard editor: a reducer + BOUNDED undo/redo history over
// `{ layout, selectedKey }`.
//
// This is boundary #1 of the two decoupled edit boundaries (the second,
// batched persistence, lands in Phase 6). Every mutation updates state
// INSTANTLY; nothing here touches the network. The whole module is PURE and
// unit-testable — the reducer is exported so tests can drive it directly.
//
// History policy (see the plan's "edit-store history policy"):
//   - `select` mutates the present but does NOT push history and does NOT set
//     dirty. It only breaks a coalescing drag gesture.
//   - `add` / `remove` / `setView` push exactly one history entry each and set
//     dirty (present ≠ saved baseline).
//   - `moveResize` COALESCES consecutive calls within an idle window into ONE
//     history entry (a drag gesture = one undo). A duplicate layout is a no-op
//     (guards against react-grid-layout's spurious on-mount onLayoutChange,
//     which would otherwise clobber the redo stack after an undo remount).
//   - `past` is bounded (cap 50) so a long session can't grow it without limit.
//   - `undo` / `redo` move the pointer; `isDirty` falls straight out of
//     present ≠ saved, so a redo/undo back to the saved snapshot reads clean.
import { useCallback, useMemo, useReducer } from "react";
import type { GridLayoutItem } from "@shared/lib/grid";

/** Bound on the undo stack — a long editing session can't grow `past` forever. */
export const HISTORY_CAP = 50;

/** Consecutive `moveResize` dispatches within this window coalesce into ONE
 * history entry (one drag gesture = one undo). A gap larger than this starts a
 * fresh entry. Kept small so two distinct drags don't merge. */
export const COALESCE_WINDOW_MS = 400;

/** The minimal shape a catalog entry must expose to be `add`ed. Kept structural
 * (not a WidgetCatalogEntry import) so the store stays domain-light. */
export interface AddEntry {
  defaultLayout?: { w: number; h: number };
  defaultView?: string;
}

/** One editable snapshot — the unit the history stack stores. */
export interface EditSnapshot {
  layout: GridLayoutItem[];
  selectedKey: string | null;
}

export type EditAction =
  | { type: "hydrate"; layout: GridLayoutItem[] }
  | { type: "moveResize"; layout: GridLayoutItem[]; at: number }
  | { type: "add"; kind: string; entry: AddEntry }
  | { type: "remove"; key: string }
  | { type: "select"; key: string | null }
  | { type: "setView"; key: string; view: string }
  | { type: "markSaved" };

interface EditHistoryState {
  past: EditSnapshot[];
  present: EditSnapshot;
  future: EditSnapshot[];
  /** Baseline the dirty check compares against — reset by hydrate / markSaved. */
  saved: GridLayoutItem[];
  /** Last action type (drives moveResize coalescing). */
  lastType: EditAction["type"] | "undo" | "redo" | null;
  /** Timestamp of the last moveResize (drives the coalesce idle window). */
  lastMoveAt: number;
  /** Bumped on every STRUCTURAL change (add/remove/setView/undo/redo/hydrate)
   * — NOT on moveResize/select. Consumers key the grid on it so add/remove/undo
   * re-seed the grid, while a live drag never remounts. */
  structuralRev: number;
}

// ---- action creators (exported for dispatch + tests) ----------------------

export const hydrate = (layout: GridLayoutItem[]): EditAction => ({
  type: "hydrate",
  layout,
});
export const moveResize = (
  layout: GridLayoutItem[],
  at: number = Date.now(),
): EditAction => ({ type: "moveResize", layout, at });
export const add = (kind: string, entry: AddEntry): EditAction => ({
  type: "add",
  kind,
  entry,
});
export const remove = (key: string): EditAction => ({ type: "remove", key });
export const select = (key: string | null): EditAction => ({ type: "select", key });
export const setView = (key: string, view: string): EditAction => ({
  type: "setView",
  key,
  view,
});
export const markSaved = (): EditAction => ({ type: "markSaved" });

// ---- pure helpers ---------------------------------------------------------

function layoutsEqual(a: GridLayoutItem[], b: GridLayoutItem[]): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** Push onto a bounded stack, dropping the oldest entry past the cap. */
function capPush(stack: EditSnapshot[], entry: EditSnapshot): EditSnapshot[] {
  const next = [...stack, entry];
  return next.length > HISTORY_CAP ? next.slice(next.length - HISTORY_CAP) : next;
}

/** Highest occupied row (y + h) — where a freshly-added tile is slotted. */
function maxY(layout: GridLayoutItem[]): number {
  return layout.reduce((m, it) => Math.max(m, it.y + it.h), 0);
}

export function initEditState(seed: GridLayoutItem[]): EditHistoryState {
  return {
    past: [],
    present: { layout: seed, selectedKey: null },
    future: [],
    saved: seed,
    lastType: null,
    lastMoveAt: 0,
    structuralRev: 0,
  };
}

// A structural mutation: snapshot the current present into `past`, clear the
// redo stack, bump the structural revision.
function commit(state: EditHistoryState, present: EditSnapshot): EditHistoryState {
  return {
    ...state,
    past: capPush(state.past, state.present),
    present,
    future: [],
    structuralRev: state.structuralRev + 1,
  };
}

export function editReducer(
  state: EditHistoryState,
  action: EditAction,
): EditHistoryState {
  switch (action.type) {
    case "hydrate":
      return {
        past: [],
        present: { layout: action.layout, selectedKey: null },
        future: [],
        saved: action.layout,
        lastType: "hydrate",
        lastMoveAt: 0,
        structuralRev: state.structuralRev + 1,
      };

    case "moveResize": {
      // No-op guard: an identical layout (RGL's on-mount reflow after an undo
      // remount) must NOT push history or clear the redo stack.
      if (layoutsEqual(action.layout, state.present.layout)) {
        return state;
      }
      const present: EditSnapshot = {
        ...state.present,
        layout: action.layout,
      };
      const coalesce =
        state.lastType === "moveResize" &&
        action.at - state.lastMoveAt <= COALESCE_WINDOW_MS;
      if (coalesce) {
        // Replace-top: keep the pre-gesture snapshot already in `past`, just
        // advance the present. One drag gesture stays one undo. NOT structural
        // — a live drag must not remount the grid.
        return { ...state, present, lastMoveAt: action.at };
      }
      // First move of a gesture: snapshot + clear redo. NOT structural (the
      // grid already holds this geometry; remounting would fight the drag).
      return {
        ...state,
        past: capPush(state.past, state.present),
        present,
        future: [],
        lastType: "moveResize",
        lastMoveAt: action.at,
      };
    }

    case "add": {
      const w = action.entry.defaultLayout?.w ?? 6;
      const h = action.entry.defaultLayout?.h ?? 3;
      const item: GridLayoutItem = {
        i: action.kind,
        x: 0,
        y: maxY(state.present.layout),
        w,
        h,
        view: action.entry.defaultView ?? null,
      };
      return {
        ...commit(state, {
          layout: [...state.present.layout, item],
          selectedKey: state.present.selectedKey,
        }),
        lastType: "add",
      };
    }

    case "remove": {
      const layout = state.present.layout.filter((w) => w.i !== action.key);
      const selectedKey =
        state.present.selectedKey === action.key ? null : state.present.selectedKey;
      return {
        ...commit(state, { layout, selectedKey }),
        lastType: "remove",
      };
    }

    case "setView": {
      const layout = state.present.layout.map((w) =>
        w.i === action.key ? { ...w, view: action.view } : w,
      );
      return {
        ...commit(state, { layout, selectedKey: state.present.selectedKey }),
        lastType: "setView",
      };
    }

    case "select":
      // Present-only; no history, no dirty. Setting lastType breaks any
      // in-progress drag-coalescing chain.
      return {
        ...state,
        present: { ...state.present, selectedKey: action.key },
        lastType: "select",
      };

    case "markSaved":
      return { ...state, saved: state.present.layout, lastType: "markSaved" };

    default:
      return state;
  }
}

function undoReducer(state: EditHistoryState): EditHistoryState {
  if (state.past.length === 0) return state;
  const prev = state.past[state.past.length - 1];
  return {
    ...state,
    past: state.past.slice(0, -1),
    present: prev,
    future: [state.present, ...state.future],
    lastType: "undo",
    structuralRev: state.structuralRev + 1,
  };
}

function redoReducer(state: EditHistoryState): EditHistoryState {
  if (state.future.length === 0) return state;
  const next = state.future[0];
  return {
    ...state,
    past: capPush(state.past, state.present),
    present: next,
    future: state.future.slice(1),
    lastType: "redo",
    structuralRev: state.structuralRev + 1,
  };
}

// Wrap the pure edit reducer with the undo/redo pointer moves so the whole
// thing is one useReducer.
type StoreAction = EditAction | { type: "undo" } | { type: "redo" };

function storeReducer(
  state: EditHistoryState,
  action: StoreAction,
): EditHistoryState {
  if (action.type === "undo") return undoReducer(state);
  if (action.type === "redo") return redoReducer(state);
  return editReducer(state, action);
}

export interface DashboardEditStore {
  state: EditSnapshot;
  dispatch: (action: EditAction) => void;
  undo: () => void;
  redo: () => void;
  canUndo: boolean;
  canRedo: boolean;
  isDirty: boolean;
  markSaved: () => void;
  selectedKey: string | null;
  setSelectedKey: (key: string | null) => void;
  /** Bumps on structural changes — key the grid on it so add/remove/undo/redo
   * re-seed the grid while a live drag never remounts. */
  structuralRev: number;
}

/** The edit store hook. `seed` is the initial layout (e.g.
 * OVERVIEW_DEFAULT_LAYOUT). Everything is local + synchronous; persistence is
 * a separate boundary (Phase 6). */
export function useDashboardEditStore(seed: GridLayoutItem[]): DashboardEditStore {
  const [full, rawDispatch] = useReducer(storeReducer, seed, initEditState);

  const dispatch = useCallback((action: EditAction) => rawDispatch(action), []);
  const undo = useCallback(() => rawDispatch({ type: "undo" }), []);
  const redo = useCallback(() => rawDispatch({ type: "redo" }), []);
  const markSavedCb = useCallback(() => rawDispatch(markSaved()), []);
  const setSelectedKey = useCallback(
    (key: string | null) => rawDispatch(select(key)),
    [],
  );

  const isDirty = useMemo(
    () => !layoutsEqual(full.present.layout, full.saved),
    [full.present.layout, full.saved],
  );

  return {
    state: full.present,
    dispatch,
    undo,
    redo,
    canUndo: full.past.length > 0,
    canRedo: full.future.length > 0,
    isDirty,
    markSaved: markSavedCb,
    selectedKey: full.present.selectedKey,
    setSelectedKey,
    structuralRev: full.structuralRev,
  };
}
