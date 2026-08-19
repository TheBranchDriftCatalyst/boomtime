// Proves the STANDALONE books composition (gaka-zp2s) is genuinely books-only:
// with VITE_BOOKS_STANDALONE=true, registerBooksAppDomains() yields a nav of
// Books + Settings ONLY (Overview is gone; Profile is a Sidebar-hardcoded link,
// not a registry item), the /app index route redirects to /app/books instead of
// rendering Overview, and no Spaces / boomtime code-domain leaf is registered.
//
// The books-only scope is gated on the VITE_BOOKS_STANDALONE build flag, which
// @shared/lib/standalone folds into a module-level const at load time. So each test
// stubs the env and re-imports the module graph fresh (vi.resetModules), then
// composes into freshly-reset registries.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RouteDef } from "@shared/shared/routing/types";

// Flatten the route registry into "parent > leaf" path strings, mirroring
// registry.test.ts (index routes shown as "<parent>/(index)").
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

async function composeStandalone() {
  const nav = await import("@shared/shared/nav/registry");
  const routing = await import("@shared/shared/routing/registry");
  nav.__resetNavRegistry();
  routing.__resetRouteRegistry();
  const { registerBooksAppDomains } = await import("@shared/app/registerBooksApp");
  registerBooksAppDomains();
  return { nav, routing };
}

describe("books standalone composition (VITE_BOOKS_STANDALONE=true)", () => {
  beforeEach(() => {
    vi.resetModules();
    vi.stubEnv("VITE_BOOKS_STANDALONE", "true");
  });
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("nav is Books + Settings ONLY — no Overview, no Spaces", async () => {
    const { nav } = await composeStandalone();
    const names = nav
      .getNavSections()
      .flatMap((s) => s.items.map((i) => i.name));
    expect([...names].sort()).toEqual(["Books", "Settings"]);
    expect(names).not.toContain("Overview");
    expect(names).not.toContain("Spaces");
  });

  it("/app index redirects to /app/books instead of rendering Overview", async () => {
    const { routing } = await composeStandalone();
    const index = routing
      .getRoutes()
      .find((r) => r.parent === "app" && r.index);
    expect(index).toBeDefined();
    // The standalone index element is <Navigate to="/app/books" replace />.
    expect((index!.element as { props?: { to?: string } }).props?.to).toBe(
      "/app/books",
    );
  });

  it("registers no Spaces route and no boomtime code-domain leaf", async () => {
    const { routing } = await composeStandalone();
    const flat = flatten(routing.getRoutes());
    // Shared shell + books surface still present.
    expect(flat).toContain("/app");
    expect(flat).toContain("/app/settings");
    expect(flat).toContain("/app/books");
    // Spaces + every boomtime leaf absent.
    for (const gone of [
      "/app/space/:id",
      "/app/projects",
      "/app/leaderboards",
      "/app/heartbeats",
      "/app/wellness",
      "/app/goals",
      "/app/import",
      "/catalog",
      "/app/admin/labels",
      "/app/admin/metrics",
    ]) {
      expect(flat).not.toContain(gone);
    }
  });
});
