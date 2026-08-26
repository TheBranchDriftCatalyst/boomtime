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
// This hook operates at the STORE boundary (hydrate in, markSaved out) —
// distinct from storeAdapter.ts, which bridges the GRID primitive to the
// store. The two boundaries compose: the grid never talks to the network,
// and this hook never talks to the grid.
import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { GridLayoutItem } from "@shared/lib/grid";
import { hydrate, type DashboardEditStore, type EditAction } from "./useDashboardEditStore";

const SAVE_DEBOUNCE_MS = 600;

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
  useEffect(() => {
    // Nothing to save while disabled, before the initial load has hydrated
    // the store (the seed-vs-defaultLayout churn on mount must never
    // trigger a PUT), or when the store isn't dirty (already at the saved
    // baseline — covers the post-save markSaved() re-render too).
    if (!enabled || !hydratedRef.current || !isDirty) return;
    const snapshot = layout;
    const timer = window.setTimeout(() => {
      void (async () => {
        try {
          await api.putDashboardLayout(scope, { cols: 12, widgets: snapshot });
          qc.invalidateQueries({ queryKey: qk.dashboardLayout(scope) });
          // Only rebaseline if nothing changed since we captured the
          // snapshot — otherwise we'd mark newer, unsaved edits as clean.
          if (layoutsEqual(layoutRef.current, snapshot)) {
            markSaved();
          }
        } catch {
          // Leave dirty — the next edit (or a future retry policy) re-fires
          // the debounce and tries again. No user-facing error surface for
          // an autosave failure; the "unsaved" indicator already
          // communicates the state honestly.
        }
      })();
    }, SAVE_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layout, isDirty, scope, enabled]);

  return { isHydrating: enabled && !hydratedRef.current };
}
