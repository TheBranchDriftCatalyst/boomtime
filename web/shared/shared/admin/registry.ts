// Shared admin-registration seam (the registry).
import type { AdminGroup, AdminGroupMeta, AdminTabDef } from "./types";

// tab id -> def. Grouping + ordering derived on read from the `group`/`order`
// metadata, so registration order never matters.
const tabs = new Map<string, AdminTabDef>();

/** Register (or replace) an admin tab. Idempotent by tab id. */
export function registerAdminTab(tab: AdminTabDef): void {
  tabs.set(tab.id, tab);
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** Flat, ordered list of every registered admin tab (group order, then tab
 *  order) — for routing/active-tab helpers that don't care about grouping. */
export function getAdminTabs(): AdminTabDef[] {
  return getAdminGroups().flatMap((g) => g.tabs);
}

/** Admin tabs bucketed into their domain groups, groups ordered by group
 *  `order` and tabs within a group by tab `order`. This drives the grouped
 *  admin tab strip. */
export function getAdminGroups(): AdminGroup[] {
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
  return Array.from(groups.values())
    .sort((a, b) => sortKey(a.order) - sortKey(b.order))
    .map((g) => ({
      ...g,
      tabs: g.tabs.sort((a, b) => sortKey(a.order) - sortKey(b.order)),
    }));
}

/** Convenience for a group's meta only (used nowhere the tabs matter). */
export function adminGroupMeta(id: string): AdminGroupMeta | undefined {
  return getAdminGroups().find((g) => g.id === id);
}

/** Test aid — clears the registry. */
export function __resetAdminRegistry(): void {
  tabs.clear();
}
