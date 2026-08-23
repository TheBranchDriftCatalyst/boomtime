// registryIdentity.test.ts — the source-side half of the TALOS-6y60 hardening.
//
// Every registration seam derives its read (grouping, ordering, flag
// resolution) from a Map. Deriving it FRESH on each call is what turned an
// ordinary `const groups = getAdminGroups()` during render into a blank page:
// the fresh array defeated the useMemo keyed on it, which handed useHeaderSlot
// an unstable node, whose identity-keyed effect then wrote on every render.
//
// So the seams now cache the derived value and invalidate it on write. The
// contract asserted here, for all four registries:
//
//   1. repeated reads return the SAME reference (callers can key memos on it)
//   2. a registration invalidates — late registration is never served stale
//
// (2) is why this is a write-invalidated cache and not a first-read memo: a
// naive cache would freeze whatever happened to be registered before the first
// render, and registration order across domains is not something a caller
// should have to reason about.
import { beforeEach, describe, expect, it } from "vitest";
import {
  __resetAdminRegistry,
  getAdminGroups,
  getAdminTabs,
  registerAdminGroup,
  registerAdminTab,
} from "@shared/shared/admin/registry";
import {
  __resetSettingsRegistry,
  getSettingsSections,
  getSettingsTabs,
  registerSettingsSection,
} from "@shared/shared/settings/registry";
import {
  __resetNavRegistry,
  getNavSections,
  registerNavItem,
  resolveNavSections,
} from "@shared/shared/nav/registry";
import {
  __resetRouteRegistry,
  getRoutes,
  registerRoute,
} from "@shared/shared/routing/registry";

// Registries are module-level singletons; this file gets its own module graph
// (vitest isolates per file), so resetting here can't bleed into other suites.
beforeEach(() => {
  __resetAdminRegistry();
  __resetSettingsRegistry();
  __resetNavRegistry();
  __resetRouteRegistry();
});

describe("admin registry", () => {
  // boom-9e9k: groups are registered separately now and tabs reference one by
  // id, so the group meta lives in exactly one place instead of being copied
  // onto every tab that belongs to it.
  const tab = (id: string) => ({
    id,
    label: id.toUpperCase(),
    to: `/app/admin/${id}`,
    group: "ops",
  });

  it("returns the same reference until a registration changes it", () => {
    registerAdminGroup({ id: "ops", label: "Operations", order: 1 });
    registerAdminTab(tab("labels"));

    const first = getAdminGroups();
    expect(getAdminGroups()).toBe(first);
    expect(getAdminTabs()).toBe(getAdminTabs());

    registerAdminTab(tab("logs"));

    const second = getAdminGroups();
    expect(second).not.toBe(first);
    expect(second[0].tabs.map((t) => t.id)).toEqual(["labels", "logs"]);
    expect(getAdminGroups()).toBe(second);
  });
});

describe("settings registry", () => {
  const section = (id: string) => ({
    id,
    label: id.toUpperCase(),
    tabs: [{ id: `${id}-tab`, label: "Tab", render: () => null }],
  });

  it("returns the same reference until a registration changes it", () => {
    registerSettingsSection(section("account"));

    const first = getSettingsSections();
    expect(getSettingsSections()).toBe(first);
    expect(getSettingsTabs()).toBe(getSettingsTabs());

    registerSettingsSection(section("books"));

    const second = getSettingsSections();
    expect(second).not.toBe(first);
    expect(second.map((s) => s.id)).toEqual(["account", "books"]);
  });
});

describe("nav registry", () => {
  const icon = () => null;

  it("returns the same reference until a registration changes it", () => {
    registerNavItem({ id: "core" }, { name: "Overview", icon, to: "/app" });

    const first = getNavSections();
    expect(getNavSections()).toBe(first);

    registerNavItem({ id: "core" }, { name: "Profile", icon, to: "/app/me" });

    expect(getNavSections()).not.toBe(first);
  });

  it("resolves flags by VALUE, so a fresh config object still hits the cache", () => {
    registerNavItem({ id: "core" }, { name: "Overview", icon, to: "/app" });
    registerNavItem(
      { id: "books" },
      { name: "Books", icon, to: "/app/books", flag: "books_enabled" },
    );

    const on = resolveNavSections({ books_enabled: true });
    // usePublicConfig hands the sidebar a NEW config object on every read, so
    // keying the cache on the object's identity would never hit — key on the
    // resolved value of the flags that are actually gated on.
    expect(resolveNavSections({ books_enabled: true })).toBe(on);
    expect(on.map((s) => s.id)).toEqual(["core", "books"]);

    const off = resolveNavSections({ books_enabled: false });
    expect(off).not.toBe(on);
    expect(off.map((s) => s.id)).toEqual(["core"]);
  });
});

describe("routing registry", () => {
  it("returns the same reference until a registration changes it", () => {
    registerRoute({ path: "app", element: null, order: 1 });

    const first = getRoutes();
    expect(getRoutes()).toBe(first);

    registerRoute({ path: "login", element: null, order: 2 });

    const second = getRoutes();
    expect(second).not.toBe(first);
    expect(second.map((r) => r.path)).toEqual(["app", "login"]);
  });
});
