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

// gaka-ebq — Admin sidebar section + /app/admin/* route tree.
//
// Verifies:
//   - Non-admins: no "Admin" entry in the sidebar; direct /app/admin/*
//     URLs bounce back to /app (AdminRoute guard).
//   - Admins: "Admin" entry visible; clicking lands on /app/admin/labels
//     (index redirect); the three sub-tabs (Labels/Backfill/Logs) each
//     drive their own URL.
//   - Legacy bookmark redirects: /app/logs, /app/settings?tab=logs,
//     /app/settings?tab=admin, /app/settings?tab=backfill each land on
//     the equivalent /app/admin/* URL.
//   - Settings page no longer surfaces the Admin/Backfill/Logs tabs.

test.describe("gaka-ebq — Admin sidebar section", () => {
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

    test("renders three sub-tabs whose URLs each activate the right body", async ({
      page,
    }) => {
      await page.goto("/app/admin/labels");
      const tablist = page.getByRole("tablist", { name: "Admin sections" });
      await expect(tablist).toBeVisible({ timeout: 10_000 });

      for (const label of ["Labels", "Backfill", "Logs"]) {
        await expect(tablist.getByRole("tab", { name: label })).toBeVisible();
      }

      // Backfill: URL update + the BackfillTab-specific heading renders.
      await tablist.getByRole("tab", { name: "Backfill" }).click();
      await expect(page).toHaveURL(/\/app\/admin\/backfill/);
      await expect(
        page.getByRole("heading", { name: /git-history backfill/i }),
      ).toBeVisible({ timeout: 10_000 });

      // Logs: URL update + Logs body rendered (embedded — no duplicate
      // "Logs" toolbar title, just the AdminPage toolbar). We assert
      // the URL flips; body specifics vary across log implementations.
      await tablist.getByRole("tab", { name: "Logs" }).click();
      await expect(page).toHaveURL(/\/app\/admin\/logs/);

      // Labels: back to the default.
      await tablist.getByRole("tab", { name: "Labels" }).click();
      await expect(page).toHaveURL(/\/app\/admin\/labels/);
    });

    test("legacy bookmark URLs redirect into /app/admin/*", async ({
      page,
    }) => {
      const cases: Array<[string, RegExp]> = [
        ["/app/logs", /\/app\/admin\/logs/],
        ["/app/settings?tab=logs", /\/app\/admin\/logs/],
        ["/app/settings?tab=admin", /\/app\/admin\/labels/],
        ["/app/settings?tab=backfill", /\/app\/admin\/backfill/],
      ];
      for (const [from, expected] of cases) {
        await page.goto(from);
        await expect(page).toHaveURL(expected, { timeout: 10_000 });
      }
    });

    test("Settings no longer surfaces Admin / Backfill / Logs tabs", async ({
      page,
    }) => {
      await page.goto("/app/settings");
      const tablist = page.getByRole("tablist", { name: "Settings sections" });
      await expect(tablist).toBeVisible({ timeout: 10_000 });

      // These three tabs moved to /app/admin. They must NOT appear here
      // any more — a regression that reintroduces them would double-list
      // Logs for admins.
      for (const gone of ["Admin", "Backfill", "Logs"]) {
        await expect(
          tablist.getByRole("tab", { name: gone, exact: true }),
        ).toHaveCount(0);
      }
    });
  });
});
