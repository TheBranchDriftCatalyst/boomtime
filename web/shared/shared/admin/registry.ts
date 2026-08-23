// Shared admin-registration seam (the registry).
//
// Two maps: groups (a domain's cluster in the section rail) and tabs. Both are
// keyed by id and idempotent, so registration ORDER never matters — grouping
// and ordering are derived on read from the `order` metadata. A domain may
// register its tabs before or after its group.
import type { AdminGroup, AdminGroupMeta, AdminTabDef } from "./types";

const groups = new Map<string, AdminGroupMeta>();
const tabs = new Map<string, AdminTabDef>();

// Derived reads are CACHED so they keep a stable identity between calls, and
// the cache is dropped on every write (see invalidate()). Stability matters:
// these arrays get fed to useMemo deps and to identity-keyed effects — a fresh
// array per call defeats the memo and thrashes the effect (TALOS-6y60 blanked
// /app/admin/* exactly that way). Callers must treat the returned value as
// IMMUTABLE — it is shared, not a copy.
let groupsCache: AdminGroup[] | null = null;
let tabsCache: AdminTabDef[] | null = null;

// Invalidating on write (rather than caching on first read) is what makes this
// safe for LATE registration: any register* call after the first render just
// drops the cache and the next read re-derives.
function invalidate(): void {
  groupsCache = null;
  tabsCache = null;
}

/**
 * Register (or replace) a domain group. Idempotent by group id.
 *
 * Groups are first-class so their label/order/icon/description live in ONE
 * place. Previously each tab carried an inline copy of its group's meta, which
 * meant N copies to keep in sync and a silent last-writer-wins on any drift.
 */
export function registerAdminGroup(group: AdminGroupMeta): void {
  groups.set(group.id, group);
  invalidate();
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
  if (tabsCache) return tabsCache;
  tabsCache = getAdminGroups().flatMap((g) => g.tabs);
  return tabsCache;
}

/** Registered meta for one group, if any. */
export function adminGroupMeta(id: string): AdminGroupMeta | undefined {
  return groups.get(id);
}

/**
 * Admin tabs bucketed into their domain groups, groups ordered by group
 * `order` and tabs within a group by tab `order`. This drives the section rail.
 * Stable identity until the registry changes; treat as immutable.
 *
 * A tab naming a group nobody registered still renders, under a synthesized
 * unlabeled group. Dropping it would be the worse failure: a domain that
 * forgets registerAdminGroup would lose its admin surface entirely, with no
 * error anywhere — exactly the kind of silent hole that took a production
 * outage to notice last time.
 */
export function getAdminGroups(): AdminGroup[] {
  if (groupsCache) return groupsCache;

  const byGroup = new Map<string, AdminTabDef[]>();
  for (const tab of tabs.values()) {
    const list = byGroup.get(tab.group);
    if (list) list.push(tab);
    else byGroup.set(tab.group, [tab]);
  }

  groupsCache = Array.from(byGroup.entries())
    .map(([id, groupTabs]) => {
      const meta = groups.get(id) ?? { id };
      return {
        ...meta,
        id,
        tabs: [...groupTabs].sort((a, b) => sortKey(a.order) - sortKey(b.order)),
      };
    })
    .sort((a, b) => sortKey(a.order) - sortKey(b.order));
  return groupsCache;
}

/** Test aid — clears both registries. */
export function __resetAdminRegistry(): void {
  tabs.clear();
  groups.clear();
  invalidate();
}
