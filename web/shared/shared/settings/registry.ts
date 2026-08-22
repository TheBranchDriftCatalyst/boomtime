// Shared settings-registration seam (the registry).
import type { SettingsSection, SettingsTab } from "./types";

const sections = new Map<string, SettingsSection>();

// Derived reads are CACHED for a stable identity between calls, dropped on
// every write. See the note in ../admin/registry.ts — same reasoning
// (TALOS-6y60): these feed useMemo deps and useHeaderSlot's identity-keyed
// effect. Callers must treat the returned value as IMMUTABLE.
let sectionsCache: SettingsSection[] | null = null;
let tabsCache: SettingsTab[] | null = null;

function invalidate(): void {
  sectionsCache = null;
  tabsCache = null;
}

/** Register (or replace) a domain's settings section. Idempotent by section id. */
export function registerSettingsSection(section: SettingsSection): void {
  sections.set(section.id, section);
  invalidate();
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** Registered sections, ordered by `order` then tabs by their `order`. Stable
 *  identity until the registry changes; treat as immutable. */
export function getSettingsSections(): SettingsSection[] {
  if (sectionsCache) return sectionsCache;
  sectionsCache = Array.from(sections.values())
    .sort((a, b) => sortKey(a.order) - sortKey(b.order))
    .map((s) => ({
      ...s,
      tabs: [...s.tabs].sort((a, b) => sortKey(a.order) - sortKey(b.order)),
    }));
  return sectionsCache;
}

/** Flat, ordered list of every registered tab across all sections — for the
 *  page's active-tab resolution (?tab=<id>). Stable identity until the registry
 *  changes; treat as immutable. */
export function getSettingsTabs(): SettingsTab[] {
  if (!tabsCache) tabsCache = getSettingsSections().flatMap((s) => s.tabs);
  return tabsCache;
}

/** Test aid — clears the registry. */
export function __resetSettingsRegistry(): void {
  sections.clear();
  invalidate();
}
