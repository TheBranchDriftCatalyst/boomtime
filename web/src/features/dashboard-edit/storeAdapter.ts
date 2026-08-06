// storeAdapter (gaka-lzr, Phase 4) — a StorageAdapter bridging the isolated
// grid primitive to the local edit store.
//
// The grid calls `storage.save(layout)` on every drag / resize / view-toggle /
// remove, and `storage.load()` once on mount. We route:
//   - save(layout) → dispatch(moveResize(layout)) — the store records the
//     geometry (coalescing a drag gesture into one undo entry).
//   - load()       → the store's CURRENT layout (via a live getter), or the
//     seed on first mount.
//
// There is NO DB in Phase 4 — persistence is a separate boundary that lands in
// Phase 6. This adapter is deliberately synchronous-over-a-ref so it can be
// memoized on `scope` (a stable identity) without re-running the grid's mount
// effect on every store update.
import type { GridLayoutItem, StorageAdapter } from "@/lib/grid";

export interface StoreAdapterOptions {
  /** Live read of the store's current layout — called by the grid on mount. */
  getLayout: () => GridLayoutItem[];
  /** Fired on every grid save; wire to `dispatch(moveResize(layout))`. */
  onSave: (layout: GridLayoutItem[]) => void;
}

export function createStoreAdapter(opts: StoreAdapterOptions): StorageAdapter {
  return {
    async load() {
      return opts.getLayout();
    },
    async save(layout) {
      opts.onSave(layout);
    },
  };
}
