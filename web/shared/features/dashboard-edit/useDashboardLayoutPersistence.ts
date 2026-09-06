// useDashboardLayoutPersistence (gaka-lzr Phase 6) — per-user, per-scope DB
// persistence for the in-app dashboard editor's local edit store
// (useDashboardEditStore). Backed by the already-shipped generic
// GET/PUT /api/v1/users/current/dashboard/:scope endpoints (gaka-keb;
// internal/spaces/dashboard_layout.go) — "overview" was admitted to the
// scope allowlist back in Phase 4 specifically so this phase wouldn't need a
// backend change. Two independent flows:
//
//   LOAD  (once, on mount): GET the persisted layout. A 404 (or any other
//         failure) means "no saved layout yet" — falls back to
//         `defaultLayout`. dispatch(hydrate(...)) seeds the store and resets
//         the undo/redo history + dirty baseline, exactly like a fresh
//         mount.
//
//   SAVE  (debounced ~600ms): fires whenever the store's `isDirty` (present
//         layout has diverged from its saved baseline). A snapshot of the
//         layout is captured at debounce-fire time; if the user kept editing
//         during the network round-trip (the live layout no longer matches
//         the snapshot that was actually PUT), `markSaved()` is skipped so
//         the store stays dirty and the NEXT debounce cycle picks up the
//         newer edits — avoids falsely clearing the "unsaved" indicator for
//         edits that were never actually persisted.
//
//         Three properties the debounce alone did not give us (audit boom-28vm):
//           * FLUSH ON UNMOUNT — an edit made <600ms before navigating away
//             used to die with the cleared timeout. The armed snapshot lives in
//             `pendingRef`, and an unmount-only effect PUTs it on the way out.
//           * BOUNDED RETRY — a failed PUT used to sit dirty until the user
//             happened to make another edit. Failures now re-arm themselves
//             (backoff, capped at MAX_SAVE_ATTEMPTS) while the store is dirty.
//           * SERIALIZED WRITES — two debounce cycles could be in flight at
//             once, and a stalled older PUT could land AFTER the newer one,
//             leaving the server on the older layout while the editor said
//             "saved". Every PUT now chains onto the previous one, so the last
//             write the client made is the last write the server sees.
//
// This hook operates at the STORE boundary (hydrate in, markSaved out) —
// distinct from storeAdapter.ts, which bridges the GRID primitive to the
// store. The two boundaries compose: the grid never talks to the network,
// and this hook never talks to the grid.
import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { GridLayoutItem } from "@shared/lib/grid";
import { hydrate, type DashboardEditStore, type EditAction } from "./useDashboardEditStore";

const SAVE_DEBOUNCE_MS = 600;
// A failed autosave re-arms itself instead of waiting for an edit that may
// never come (the user is usually on their way out of the page). Bounded, with
// backoff, so a hard-down backend can't turn autosave into a PUT loop.
const MAX_SAVE_ATTEMPTS = 3;
const RETRY_BASE_MS = 1_200;
const RETRY_MAX_MS = 5_000;

interface LayoutEnvelope {
  cols?: number;
  widgets?: GridLayoutItem[];
}

function layoutsEqual(a: GridLayoutItem[], b: GridLayoutItem[]): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export interface UseDashboardLayoutPersistenceResult {
  /** True until the initial GET has settled (success OR "no saved layout")
   * and the store has been hydrated from it. Informational — the store's
   * OWN seed already renders something reasonable (defaultLayout) while
   * this is true, so callers aren't required to gate on it. */
  isHydrating: boolean;
}

export interface DashboardLayoutPersistenceStore {
  state: DashboardEditStore["state"];
  dispatch: (action: EditAction) => void;
  isDirty: boolean;
  markSaved: () => void;
}

export interface UseDashboardLayoutPersistenceOptions {
  /** Gates BOTH the GET (react-query `enabled`) and the debounced PUT. The
   * editor hook (useDashboardEditor) is called unconditionally by
   * OverviewDashboard regardless of the `overviewEditor` flag — without this
   * gate, every page load would fire a real network GET even for the
   * overwhelming majority of users who never see the editor. Default true. */
  enabled?: boolean;
}

/** Wires DB persistence for `scope` onto an existing edit store. Call once
 * per store instance (e.g. from useDashboardEditor). */
export function useDashboardLayoutPersistence(
  scope: string,
  store: DashboardLayoutPersistenceStore,
  defaultLayout: GridLayoutItem[],
  opts: UseDashboardLayoutPersistenceOptions = {},
): UseDashboardLayoutPersistenceResult {
  const { enabled = true } = opts;
  const { state, dispatch, isDirty, markSaved } = store;
  const layout = state.layout;

  // Live ref so the save effect's async callback can tell whether the
  // layout it's about to mark saved is still current when the PUT resolves.
  const layoutRef = useRef(layout);
  layoutRef.current = layout;

  const hydratedRef = useRef(false);

  // ---- LOAD (once, only while enabled) ----
  const query = useQuery({
    queryKey: qk.dashboardLayout(scope),
    queryFn: () => api.getDashboardLayout(scope),
    retry: false,
    enabled,
    // A saved layout only ever changes from THIS editor; a surprise
    // window-focus refetch mid-edit would fight the user's live drag.
    refetchOnWindowFocus: false,
  });

  useEffect(() => {
    // Do NOT flip hydratedRef while disabled — if `enabled` flips true later
    // in the same session (the user turns the flag on without a reload), the
    // load must still be allowed to happen and hydrate the store.
    if (!enabled) return;
    if (hydratedRef.current) return;
    if (query.isLoading) return;
    hydratedRef.current = true;
    // query.isError (404 = "nothing saved yet") falls through the same
    // branch as a genuinely-empty envelope — `persisted` is undefined
    // either way, so no special-casing is needed here.
    const envelope = query.data?.layout as LayoutEnvelope | undefined;
    const persisted = envelope?.widgets;
    dispatch(hydrate(persisted && persisted.length > 0 ? persisted : defaultLayout));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, query.isLoading]);

  // ---- SAVE (debounced, only while enabled) ----
  const qc = useQueryClient();

  // Live refs for everything the save path touches, so the unmount flush — an
  // effect whose cleanup runs ONLY on unmount and therefore closes over the
  // FIRST render's values — still writes the current scope/store.
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const markSavedRef = useRef(markSaved);
  markSavedRef.current = markSaved;
  const qcRef = useRef(qc);
  qcRef.current = qc;

  // The snapshot that has been armed (or attempted and failed) but is not yet
  // on the server. Non-null == "there is unsaved work the unmount flush must
  // push". Claimed (set to null) when its PUT starts, restored if that fails.
  const pendingRef = useRef<GridLayoutItem[] | null>(null);
  // Tail of the PUT chain: every save awaits the previous one, so writes reach
  // the server in the order the client issued them.
  const chainRef = useRef<Promise<void>>(Promise.resolve());
  const attemptsRef = useRef(0);
  const armedRef = useRef<GridLayoutItem[] | null>(null);
  const mountedRef = useRef(true);
  // Bumping this re-runs the debounce effect after a failed PUT.
  const [retryTick, setRetryTick] = useState(0);

  const save = useCallback((snapshot: GridLayoutItem[]): Promise<void> => {
    const run = chainRef.current.then(async () => {
      // Claim the snapshot so an unmount mid-flight doesn't double-PUT it.
      if (pendingRef.current === snapshot) pendingRef.current = null;
      try {
        await api.putDashboardLayout(scopeRef.current, { cols: 12, widgets: snapshot });
        attemptsRef.current = 0;
        qcRef.current.invalidateQueries({
          queryKey: qk.dashboardLayout(scopeRef.current),
        });
        // Only rebaseline if nothing changed since we captured the
        // snapshot — otherwise we'd mark newer, unsaved edits as clean.
        if (layoutsEqual(layoutRef.current, snapshot)) {
          markSavedRef.current();
        }
      } catch {
        // Leave the store dirty and put the snapshot back on the pending slot
        // (unless a newer edit already claimed it) so the bounded retry below
        // — and the unmount flush — still have something to push. No
        // user-facing error surface for an autosave failure; the "unsaved"
        // indicator already communicates the state honestly.
        attemptsRef.current += 1;
        if (pendingRef.current === null) pendingRef.current = snapshot;
        if (mountedRef.current && attemptsRef.current < MAX_SAVE_ATTEMPTS) {
          setRetryTick((t) => t + 1);
        }
      }
    });
    chainRef.current = run;
    return run;
  }, []);

  useEffect(() => {
    // Nothing to save while disabled, before the initial load has hydrated
    // the store (the seed-vs-defaultLayout churn on mount must never
    // trigger a PUT), or when the store isn't dirty (already at the saved
    // baseline — covers the post-save markSaved() re-render too).
    if (!enabled || !hydratedRef.current || !isDirty) return;
    const snapshot = layout;
    // A genuinely new edit restarts the backoff; a retry re-run keeps it.
    if (armedRef.current !== snapshot) {
      armedRef.current = snapshot;
      attemptsRef.current = 0;
    }
    pendingRef.current = snapshot;
    const delay =
      attemptsRef.current === 0
        ? SAVE_DEBOUNCE_MS
        : Math.min(RETRY_BASE_MS * 2 ** (attemptsRef.current - 1), RETRY_MAX_MS);
    const timer = window.setTimeout(() => void save(snapshot), delay);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layout, isDirty, scope, enabled, retryTick]);

  // ---- FLUSH (unmount only) ----
  // `save` is stable, so this cleanup runs exactly once, on unmount: the last
  // edit before a navigation is PUT instead of dying with the cleared timer.
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      const pending = pendingRef.current;
      if (pending) void save(pending);
    };
  }, [save]);

  return { isHydrating: enabled && !hydratedRef.current };
}
