// dbStorageAdapter — StorageAdapter implementation for the isolated grid
// primitive backed by boomtime's /api/v1/users/current/dashboard/:scope
// endpoints. Lives OUTSIDE the primitive folder (which stays boomtime-
// agnostic).
import { api, ApiError } from "@/lib/api";
import type { GridLayoutItem, StorageAdapter } from "@/lib/grid";

interface LayoutEnvelope {
  cols?: number;
  widgets?: GridLayoutItem[];
}

/** Build an adapter for a given scope. `onSaved` is called after a
 * successful save so the caller can invalidate its react-query cache /
 * toast a success message. */
export function dbStorageAdapter(scope: string, opts: { onSaved?: () => void } = {}): StorageAdapter {
  return {
    async load() {
      try {
        const resp = await api.getDashboardLayout(scope);
        const l = resp?.layout as LayoutEnvelope | undefined;
        return l?.widgets ?? null;
      } catch (e) {
        // 404 = "no layout saved yet" — bubble up as null so the primitive
        // uses defaults. Other errors bubble up as null too; the user's
        // first save will overwrite whatever might be stuck.
        if (e instanceof ApiError && e.status === 404) return null;
        return null;
      }
    },
    async save(items) {
      await api.putDashboardLayout(scope, { cols: 12, widgets: items });
      opts.onSaved?.();
    },
  };
}

/** Preload-hydrating variant: seed the initial layout from a payload the
 * page already fetched (e.g. the public profile response embeds the
 * layout), avoiding a second network round-trip. Save still hits the DB. */
export function hydratedStorageAdapter(
  scope: string,
  seed: LayoutEnvelope | undefined,
  opts: { onSaved?: () => void } = {},
): StorageAdapter {
  const seedItems = seed?.widgets ?? null;
  const inner = dbStorageAdapter(scope, opts);
  return {
    async load() {
      return seedItems;
    },
    save: inner.save,
  };
}
