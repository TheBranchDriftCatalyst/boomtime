// Shared nav-registration seam (the registry).
//
// A mutable, module-level registry the shell OWNS but never populates. Domains
// call registerNavItem() from their own registration module; the host app entry
// composes which domains register (see web/src/app/registerDomains.ts). The
// shell imports no domain code — that inversion is what lets an isolated books
// build drop all boomtime modules.
import type { NavFlag, NavItem, NavSection, NavSectionMeta } from "./types";

// section id -> resolved section. Insertion-independent: final order comes from
// the `order` fields, so registration order never matters.
const sections = new Map<string, NavSection>();

/** Register a single nav destination into a (possibly new) section. Idempotent:
 *  re-registering the same section+route replaces the prior entry, so repeated
 *  registration (HMR, test setup re-import) never duplicates. The latest section
 *  meta wins, so a domain can set the section's label/order alongside its items. */
export function registerNavItem(section: NavSectionMeta, item: NavItem): void {
  const existing = sections.get(section.id);
  if (existing) {
    // Refresh section meta (label/order) from the latest registration.
    existing.label = section.label ?? existing.label;
    existing.order = section.order ?? existing.order;
    const i = existing.items.findIndex((it) => it.to === item.to);
    if (i >= 0) existing.items[i] = item;
    else existing.items.push(item);
  } else {
    sections.set(section.id, {
      id: section.id,
      label: section.label,
      order: section.order,
      items: [item],
    });
  }
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** All registered sections, ordered by section `order` then items by item
 *  `order`. Read at render time — registration has already happened at entry. */
export function getNavSections(): NavSection[] {
  return Array.from(sections.values())
    .sort((a, b) => sortKey(a.order) - sortKey(b.order))
    .map((s) => ({
      ...s,
      items: [...s.items].sort((a, b) => sortKey(a.order) - sortKey(b.order)),
    }));
}

/** Resolve the registry against the loaded public-config flags: drop flag-gated
 *  items whose flag is off, then drop any section left empty. This is what the
 *  sidebar renders, in order. */
export function resolveNavSections(
  config: Partial<Record<NavFlag, boolean>>,
): NavSection[] {
  return getNavSections()
    .map((s) => ({
      ...s,
      items: s.items.filter((i) => !i.flag || config[i.flag]),
    }))
    .filter((s) => s.items.length > 0);
}

/** Test aid — clears the registry so a test can register a known fixture set. */
export function __resetNavRegistry(): void {
  sections.clear();
}
