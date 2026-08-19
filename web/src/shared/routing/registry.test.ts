// Proves the registration-driven router reproduces the host app's exact route
// table (gaka-zp2s). The shell (App.tsx) builds its <Routes> from getRoutes();
// this test asserts that after the host composition registers all three domains
// (done in test setup via registerHostDomains), the flattened path tree is
// byte-for-byte what the old static <Routes> list declared — so the refactor is
// behavior-preserving — AND that a books-only composition drops every boomtime
// leaf while keeping the shared shell.
import { describe, expect, it, afterEach } from "vitest";

import { getRoutes, registerRoute, __resetRouteRegistry } from "./registry";
import type { RouteDef } from "./types";
import { registerCoreDomain } from "@/domains/core/register";
import { registerBooksDomain } from "@/domains/books/register";
import { registerBoomtimeDomain } from "@/domains/boomtime/register";

// Flatten the registry into "parent > leaf" path strings (index routes shown as
// "<parent>/(index)"), ordered the way App.tsx would nest them.
function flatten(defs: RouteDef[]): string[] {
  const walk = (parent?: string, prefix = ""): string[] =>
    defs
      .filter((d) => (d.parent ?? undefined) === parent)
      .flatMap((d) => {
        const seg = d.index ? "(index)" : (d.path ?? "");
        const label = `${prefix}${seg}`;
        const kids = d.id ? walk(d.id, `${label}/`) : [];
        return [label, ...kids];
      });
  return walk();
}

afterEach(() => {
  // Restore the host composition that setup.ts installed, so sibling test files
  // sharing this module's registry see the full route table.
  __resetRouteRegistry();
  registerCoreDomain();
  registerBooksDomain();
  registerBoomtimeDomain();
});

describe("route registry — host composition", () => {
  it("reproduces the exact host route tree", () => {
    __resetRouteRegistry();
    registerCoreDomain();
    registerBooksDomain();
    registerBoomtimeDomain();

    expect(flatten(getRoutes())).toEqual([
      "/",
      "/login",
      "/register",
      "/onboarding",
      "/p/:slug",
      "/catalog",
      "/app",
      "/app/(index)",
      "/app/projects",
      "/app/profile",
      "/app/goals",
      "/app/books",
      "/app/leaderboards",
      "/app/heartbeats",
      "/app/space/:id",
      "/app/import",
      "/app/logs",
      "/app/changelog",
      "/app/admin",
      "/app/admin/(index)",
      "/app/admin/users",
      "/app/admin/labels",
      "/app/admin/cli",
      "/app/admin/jobs",
      "/app/admin/metrics",
      "/app/admin/data",
      "/app/admin/books",
      "/app/admin/logs",
      "/app/settings",
      "/app/wellness",
      "/app/catalog",
      "*",
    ]);
  });
});

describe("route registry — books-only composition", () => {
  it("keeps the shared shell + books leaves, drops every boomtime leaf", () => {
    __resetRouteRegistry();
    registerCoreDomain();
    registerBooksDomain();

    const flat = flatten(getRoutes());
    // Shared shell + books present.
    expect(flat).toContain("/app");
    expect(flat).toContain("/app/(index)"); // Overview
    expect(flat).toContain("/app/settings");
    expect(flat).toContain("/app/books");
    expect(flat).toContain("/app/admin/books");
    // Every boomtime leaf absent.
    for (const gone of [
      "/catalog",
      "/app/projects",
      "/app/leaderboards",
      "/app/heartbeats",
      "/app/wellness",
      "/app/goals",
      "/app/import",
      "/app/space/:id",
      "/app/catalog",
      "/app/admin/labels",
      "/app/admin/metrics",
    ]) {
      expect(flat).not.toContain(gone);
    }
  });
});

describe("route registry — idempotency", () => {
  it("replaces a re-registered slot instead of duplicating", () => {
    __resetRouteRegistry();
    registerRoute({ parent: "app", path: "x", element: null, order: 1 });
    registerRoute({ parent: "app", path: "x", element: null, order: 2 });
    expect(getRoutes().filter((r) => r.path === "x")).toHaveLength(1);
  });
});
