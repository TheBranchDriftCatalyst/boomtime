import { expect, test } from "@playwright/test";
import {
  ADMIN_PASSWORD,
  ADMIN_USERNAME,
  NO_ADMIN_CREDS_REASON,
  NO_STACK_REASON,
  loginAsAdmin,
  loginAsNonAdmin,
  stackReachableFromEnv,
} from "./helpers";

// boom-ebq — Admin sidebar section + /app/admin/* route tree.
//
// Verifies:
//   - Non-admins: no "Admin" entry in the sidebar; direct /app/admin/*
//     URLs bounce back to /app (AdminRoute guard).
//   - Admins: "Admin" entry visible; clicking lands on /app/admin/labels
//     (index redirect); the sub-tabs (Labels/Logs) each drive their own URL.
//   - Legacy bookmark redirects: /app/logs, /app/settings?tab=logs,
//     /app/settings?tab=admin each land on the equivalent /app/admin/* URL.
//   - Settings page no longer surfaces the Admin/Logs tabs.

test.describe("boom-ebq — Admin sidebar section", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);

  test.describe("as a non-admin user", () => {
    test("hides the sidebar Admin entry and bounces direct URLs", async ({
      page,
    }) => {
      await loginAsNonAdmin(page);
      await page.goto("/app");
      await expect(page).toHaveURL(/\/app(\/|$)/);

      // The Admin nav entry uses data-testid="sidebar-admin" via
      // AdminNavLink and only renders when isAdmin === true. For a
      // non-admin it must be absent — not merely hidden.
      await expect(page.locator('[data-testid="sidebar-admin"]')).toHaveCount(
        0,
      );

      // Direct URL bounces via AdminRoute → /app. Wait a beat: the
      // useIsAdmin() query resolves before AdminRoute picks a branch.
      await page.goto("/app/admin/labels");
      await expect(page).toHaveURL(/\/app\/?$/, { timeout: 10_000 });
    });
  });

  test.describe("as an admin user", () => {
    test.skip(
      !ADMIN_USERNAME || !ADMIN_PASSWORD,
      NO_ADMIN_CREDS_REASON,
    );

    test.beforeEach(async ({ page }) => {
      const ok = await loginAsAdmin(page);
      test.skip(!ok, NO_ADMIN_CREDS_REASON);
    });

    test("renders the Admin nav link", async ({ page }) => {
      await page.goto("/app");
      const adminLink = page.locator('[data-testid="sidebar-admin"]');
      await expect(adminLink).toBeVisible({ timeout: 10_000 });
      // The link's accessible name is "Admin" (aria-label).
      await expect(adminLink).toHaveAttribute("aria-label", "Admin");
    });

    test("clicking Admin lands on /app/admin/labels via the index redirect", async ({
      page,
    }) => {
      await page.goto("/app");
      await page.locator('[data-testid="sidebar-admin"]').click();
      await expect(page).toHaveURL(/\/app\/admin\/labels/, {
        timeout: 10_000,
      });
    });

    test("renders sub-entries whose URLs each activate the right body", async ({
      page,
    }) => {
      await page.goto("/app/admin/labels");
      // boom-4x33: the section nav is a vertical RAIL inside the content
      // column now, not a tab strip hoisted into the app header — so it's a
      // navigation landmark of links rather than a tablist of tabs.
      const rail = page.getByRole("navigation", { name: "Admin sections" });
      await expect(rail).toBeVisible({ timeout: 10_000 });

      for (const label of ["Labels", "Logs"]) {
        await expect(rail.getByRole("link", { name: label })).toBeVisible();
      }

      // Logs: URL update. We assert the URL flips; body specifics vary across
      // log implementations.
      await rail.getByRole("link", { name: "Logs" }).click();
      await expect(page).toHaveURL(/\/app\/admin\/logs/);

      // The shell titles the page from the ACTIVE entry's registry metadata,
      // so the heading must track the rail selection — that binding is the
      // whole point of the registry-driven header (boom-9e9k).
      await expect(
        page.getByRole("heading", { name: "Logs", level: 1 }),
      ).toBeVisible();

      // Labels: back to the default.
      await rail.getByRole("link", { name: "Labels" }).click();
      await expect(page).toHaveURL(/\/app\/admin\/labels/);
      await expect(
        page.getByRole("heading", { name: "Labels", level: 1 }),
      ).toBeVisible();
    });

    test("exposes an API Docs tab pointing at the self-hosted Swagger UI", async ({
      page,
    }) => {
      await page.goto("/app/admin/labels");

      // Registered as an EXTERNAL entry: the Swagger UI is served by the Go
      // binary, not the router, so this must be a real anchor that opens a new
      // tab — a NavLink here would hand /api/docs/ to react-router.
      const docs = page.getByRole("link", { name: /API Docs/ });
      await expect(docs).toBeVisible({ timeout: 10_000 });
      await expect(docs).toHaveAttribute("target", "_blank");
      const href = await docs.getAttribute("href");
      expect(href).toBe("/api/docs/");

      // Follow it for real. Asserting the href alone would still pass if the
      // route were missing server-side — the SPA catch-all answers unknown
      // paths with index.html and a 200. Only the mounted UI proves it lands.
      await page.goto(href!);
      await page.waitForSelector("#swagger-ui", { timeout: 15_000 });
    });

    test("legacy bookmark URLs redirect into /app/admin/*", async ({
      page,
    }) => {
      const cases: Array<[string, RegExp]> = [
        ["/app/logs", /\/app\/admin\/logs/],
        ["/app/settings?tab=logs", /\/app\/admin\/logs/],
        ["/app/settings?tab=admin", /\/app\/admin\/labels/],
      ];
      for (const [from, expected] of cases) {
        await page.goto(from);
        await expect(page).toHaveURL(expected, { timeout: 10_000 });
      }
    });

    test("Settings no longer surfaces Admin / Logs tabs", async ({
      page,
    }) => {
      await page.goto("/app/settings");
      const rail = page.getByRole("navigation", { name: "Settings sections" });
      await expect(rail).toBeVisible({ timeout: 10_000 });

      // These tabs moved to /app/admin. They must NOT appear here any
      // more — a regression that reintroduces them would double-list
      // Logs for admins.
      for (const gone of ["Admin", "Logs"]) {
        await expect(
          rail.getByRole("button", { name: gone, exact: true }),
        ).toHaveCount(0);
      }
    });
  });
});
