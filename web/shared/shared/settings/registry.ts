// Shared settings-registration seam (the registry).
import type { SettingsSection, SettingsTab } from "./types";

const sections = new Map<string, SettingsSection>();

/** Register (or replace) a domain's settings section. Idempotent by section id. */
export function registerSettingsSection(section: SettingsSection): void {
  sections.set(section.id, section);
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** Registered sections, ordered by `order` then tabs by their `order`. */
export function getSettingsSections(): SettingsSection[] {
  return Array.from(sections.values())
    .sort((a, b) => sortKey(a.order) - sortKey(b.order))
    .map((s) => ({
      ...s,
      tabs: [...s.tabs].sort((a, b) => sortKey(a.order) - sortKey(b.order)),
    }));
}

/** Flat, ordered list of every registered tab across all sections — for the
 *  page's active-tab resolution (?tab=<id>). */
export function getSettingsTabs(): SettingsTab[] {
  return getSettingsSections().flatMap((s) => s.tabs);
}

/** Test aid — clears the registry. */
export function __resetSettingsRegistry(): void {
  sections.clear();
}
