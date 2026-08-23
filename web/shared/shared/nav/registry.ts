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

// Derived reads are CACHED for a stable identity between calls, dropped on
// every write. See the note in ../admin/registry.ts — same reasoning
// (TALOS-6y60): a fresh array per call defeats every useMemo keyed on it and
// re-runs identity-keyed effects (useHeaderSlot) on every render. Callers must
// treat the returned value as IMMUTABLE.
let sectionsCache: NavSection[] | null = null;
let resolvedCache: { key: string; value: NavSection[] } | null = null;

function invalidate(): void {
  sectionsCache = null;
  resolvedCache = null;
}

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
  invalidate();
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** All registered sections, ordered by section `order` then items by item
 *  `order`. Read at render time — registration has already happened at entry.
 *  Stable identity until the registry changes; treat as immutable. */
export function getNavSections(): NavSection[] {
  if (sectionsCache) return sectionsCache;
  sectionsCache = Array.from(sections.values())
    .sort((a, b) => sortKey(a.order) - sortKey(b.order))
    .map((s) => ({
      ...s,
      items: [...s.items].sort((a, b) => sortKey(a.order) - sortKey(b.order)),
    }));
  return sectionsCache;
}

/** Resolve the registry against the loaded public-config flags AND the caller's
 *  admin status: drop flag-gated items whose flag is off and admin-only items
 *  for non-admins, then drop any section left empty. This is what the
 *  sidebar renders, in order. Stable identity for as long as the registry AND
 *  the flags it actually gates on are unchanged (the config object itself gets
 *  a new identity on every react-query read, so keying on the object would
 *  never hit); treat as immutable. */
export function resolveNavSections(
  config: Partial<Record<NavFlag, boolean>>,
  isAdmin = false,
): NavSection[] {
  const all = getNavSections();
  // Key on the resolved value of every flag any registered item gates on —
  // that, plus `isAdmin` and the registry itself, is the complete input to the
  // filter below. `isAdmin` joins the key unconditionally: it is one character
  // and getting it wrong would serve a cached non-admin nav to an admin (or
  // worse, the reverse) for the life of the process.
  const flags = new Set<NavFlag>();
  for (const s of all) for (const i of s.items) if (i.flag) flags.add(i.flag);
  const key = [
    `admin:${isAdmin ? "1" : "0"}`,
    ...[...flags].sort().map((f) => `${f}:${config[f] ? "1" : "0"}`),
  ].join(",");
  if (resolvedCache?.key === key) return resolvedCache.value;
  const value = all
    .map((s) => ({
      ...s,
      items: s.items.filter(
        (i) => (!i.flag || config[i.flag]) && (!i.adminOnly || isAdmin),
      ),
    }))
    .filter((s) => s.items.length > 0);
  resolvedCache = { key, value };
  return value;
}

/** Test aid — clears the registry so a test can register a known fixture set. */
export function __resetNavRegistry(): void {
  sections.clear();
  invalidate();
}
