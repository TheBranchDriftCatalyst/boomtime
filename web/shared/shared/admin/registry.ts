// Shared admin-registration seam (the registry).
import type { AdminGroup, AdminGroupMeta, AdminTabDef } from "./types";

// tab id -> def. Grouping + ordering derived on read from the `group`/`order`
// metadata, so registration order never matters.
const tabs = new Map<string, AdminTabDef>();

// Derived reads are CACHED so they keep a stable identity between calls, and
// the cache is dropped on every write (see invalidate()). Stability matters:
// these arrays get fed to useMemo deps and to useHeaderSlot, whose effect is
// keyed on its argument's identity — a fresh array per call defeats the memo
// and thrashes the effect (TALOS-6y60 blanked /app/admin/* exactly that way).
// Callers must treat the returned value as IMMUTABLE — it is shared, not a copy.
let groupsCache: AdminGroup[] | null = null;
let tabsCache: AdminTabDef[] | null = null;

// Invalidating on write (rather than caching on first read) is what makes this
// safe for LATE registration: any registerAdminTab after the first render just
// drops the cache and the next read re-derives.
function invalidate(): void {
  groupsCache = null;
  tabsCache = null;
}

/** Register (or replace) an admin tab. Idempotent by tab id. */
export function registerAdminTab(tab: AdminTabDef): void {
  tabs.set(tab.id, tab);
  invalidate();
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** Flat, ordered list of every registered admin tab (group order, then tab
 *  order) — for routing/active-tab helpers that don't care about grouping.
 *  Stable identity until the registry changes; treat as immutable. */
export function getAdminTabs(): AdminTabDef[] {
  if (!tabsCache) tabsCache = getAdminGroups().flatMap((g) => g.tabs);
  return tabsCache;
}

/** Admin tabs bucketed into their domain groups, groups ordered by group
 *  `order` and tabs within a group by tab `order`. This drives the grouped
 *  admin tab strip. Stable identity until the registry changes; treat as
 *  immutable. */
export function getAdminGroups(): AdminGroup[] {
  if (groupsCache) return groupsCache;
  const groups = new Map<string, AdminGroup>();
  for (const tab of tabs.values()) {
    const g = groups.get(tab.group.id);
    if (g) {
      g.label = tab.group.label ?? g.label;
      g.order = tab.group.order ?? g.order;
      g.tabs.push(tab);
    } else {
      groups.set(tab.group.id, {
        id: tab.group.id,
        label: tab.group.label,
        order: tab.group.order,
        tabs: [tab],
      });
    }
  }
  groupsCache = Array.from(groups.values())
    .sort((a, b) => sortKey(a.order) - sortKey(b.order))
    .map((g) => ({
      ...g,
      tabs: g.tabs.sort((a, b) => sortKey(a.order) - sortKey(b.order)),
    }));
  return groupsCache;
}

/** Convenience for a group's meta only (used nowhere the tabs matter). */
export function adminGroupMeta(id: string): AdminGroupMeta | undefined {
  return getAdminGroups().find((g) => g.id === id);
}

/** Test aid — clears the registry. */
export function __resetAdminRegistry(): void {
  tabs.clear();
  invalidate();
}
