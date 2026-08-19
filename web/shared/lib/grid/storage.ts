// storage — persistence adapters for the isolated grid primitive. The
// primitive ships with a localStorage adapter for parity with hakboard;
// consumers implement other adapters (DB, IndexedDB, ...) in their own
// codebase.
import type { GridLayoutItem, StorageAdapter } from "./types";

/** localStorage-backed adapter — safe on SSR (bails to no-op if `window`
 * is undefined). Keys are namespaced by the caller-supplied dashboard id. */
export function localStorageAdapter(dashboardId: string): StorageAdapter {
  const key = `layout:${dashboardId}`;
  return {
    async load() {
      if (typeof window === "undefined") return null;
      const raw = window.localStorage.getItem(key);
      if (!raw) return null;
      try {
        const parsed = JSON.parse(raw) as GridLayoutItem[];
        if (!Array.isArray(parsed)) return null;
        return parsed;
      } catch {
        return null;
      }
    },
    async save(layout) {
      if (typeof window === "undefined") return;
      window.localStorage.setItem(key, JSON.stringify(layout));
    },
  };
}

/** In-memory adapter — no persistence. Useful for previews and tests. */
export function memoryAdapter(initial: GridLayoutItem[] = []): StorageAdapter {
  let state: GridLayoutItem[] = [...initial];
  return {
    async load() {
      return state;
    },
    async save(layout) {
      state = [...layout];
    },
  };
}
