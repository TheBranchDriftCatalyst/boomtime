import { expect, test, type Page } from "@playwright/test";
import {
  ADMIN_PASSWORD,
  ADMIN_USERNAME,
  NO_ADMIN_CREDS_REASON,
  NO_STACK_REASON,
  loginAsAdmin,
  stackReachableFromEnv,
} from "./helpers";

// Admin CLI-runner — /app/admin/cli "Commands" tab (BOOM_FEATURE_ADMIN_CLI).
//
// Backend feature flag: the three /api/v1/admin/cli/* routes are only
// registered when BOOM_FEATURE_ADMIN_CLI=on is set in the boomtime server
// environment (dev stack: add it to .env and restart). When the flag is off
// the spec endpoint 404s and the tab renders its "CLI runner is disabled"
// card — these specs detect that state and SKIP the run/autocomplete
// assertions rather than failing, so CI never hard-fails on a disabled
// feature. The tab itself must exist either way.
//
// Verifies (flag on):
//   - "Commands" entry present in the admin rail; /app/admin/cli routes.
//   - `user list` (readonly): Run produces an output panel.
//   - `user show`: typing in the username combobox surfaces cobra-powered
//     suggestions.
//   - `backfill last-context` (mutating + dry-run): Dry run toggle defaults
//     ON; flipping it off turns Run into Apply… behind a typed-confirm gate.

/**
 * Navigate to the Commands tab and report whether the backend feature is
 * enabled (true) or the disabled card is showing (false).
 */
async function gotoCliTab(page: Page): Promise<boolean> {
  await page.goto("/app/admin/cli");
  const disabledCard = page.getByText("CLI runner is disabled");
  const commandList = page.getByRole("navigation", { name: "Commands" });
  await expect(disabledCard.or(commandList).first()).toBeVisible({
    timeout: 10_000,
  });
  return !(await disabledCard.isVisible());
}

test.describe("admin CLI runner — Commands tab", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);
  test.skip(!ADMIN_USERNAME || !ADMIN_PASSWORD, NO_ADMIN_CREDS_REASON);

  test.beforeEach(async ({ page }) => {
    const ok = await loginAsAdmin(page);
    test.skip(!ok, NO_ADMIN_CREDS_REASON);
  });

  test("the Commands tab is present and renders (enabled list or disabled card)", async ({
    page,
  }) => {
    await page.goto("/app/admin/labels");
    // gaka-4x33: vertical rail (a navigation landmark of links), not a tablist.
    const rail = page.getByRole("navigation", { name: "Admin sections" });
    await expect(rail).toBeVisible({ timeout: 10_000 });
    await expect(
      rail.getByRole("link", { name: "Commands" }),
    ).toBeVisible();

    await rail.getByRole("link", { name: "Commands" }).click();
    await expect(page).toHaveURL(/\/app\/admin\/cli/);
    // Whichever state the backend flag dictates, the tab must render a
    // deliberate body — never a crash/blank.
    const enabled = await gotoCliTab(page);
    if (!enabled) {
      await expect(
        page.getByText("BOOM_FEATURE_ADMIN_CLI=on"),
      ).toBeVisible();
    }
  });

  test("user list runs and shows captured output", async ({ page }) => {
    const enabled = await gotoCliTab(page);
    test.skip(!enabled, "BOOM_FEATURE_ADMIN_CLI is off on this stack");

    await page.getByRole("button", { name: /^user list/ }).click();
    await page.getByRole("button", { name: /^Run$/ }).click();

    // The run now streams into a live terminal viewer (gaka-hney.5); the
    // output tails in over the WS, and toContainText retries until it lands.
    const panel = page.getByTestId("terminal-log-viewer");
    await expect(panel).toBeVisible({ timeout: 15_000 });
    // The admin fixture user must be in the listing.
    await expect(panel).toContainText(ADMIN_USERNAME, { timeout: 15_000 });
  });

  test("user show offers cobra-powered username autocomplete", async ({
    page,
  }) => {
    const enabled = await gotoCliTab(page);
    test.skip(!enabled, "BOOM_FEATURE_ADMIN_CLI is off on this stack");

    await page.getByRole("button", { name: /^user show/ }).click();
    const input = page.getByRole("combobox", { name: /username/ });
    await input.pressSequentially(ADMIN_USERNAME.slice(0, 2), { delay: 50 });

    // The completer lists usernames from the DB; the admin fixture user
    // must appear as a suggestion after the ~200ms debounce.
    const listbox = page.getByRole("listbox");
    await expect(listbox).toBeVisible({ timeout: 10_000 });
    await expect(
      listbox.getByRole("option", { name: new RegExp(ADMIN_USERNAME) }),
    ).toBeVisible();
  });

  test("backfill last-context: dry-run defaults ON and applying is confirm-gated", async ({
    page,
  }) => {
    const enabled = await gotoCliTab(page);
    test.skip(!enabled, "BOOM_FEATURE_ADMIN_CLI is off on this stack");

    await page.getByRole("button", { name: /backfill last-context/ }).click();

    // Dry run toggle defaults ON; the action is a dry-run Run.
    const toggle = page.getByRole("switch", { name: /dry run/i });
    await expect(toggle).toBeVisible();
    await expect(toggle).toBeChecked();
    await expect(
      page.getByRole("button", { name: /run \(dry-run\)/i }),
    ).toBeVisible();

    // Flipping dry-run off arms the Apply path behind the typed-confirm
    // dialog. We assert the gate and CANCEL — the e2e suite must not
    // mutate the shared stack's data.
    await toggle.click();
    await page.getByRole("button", { name: /apply…/i }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: /^Apply$/ }),
    ).toBeDisabled();

    // Wrong sentinel keeps the gate shut; the exact command arms it.
    const confirm = dialog.getByPlaceholder("backfill last-context");
    await confirm.fill("nope");
    await expect(
      dialog.getByRole("button", { name: /^Apply$/ }),
    ).toBeDisabled();
    await confirm.fill("backfill last-context");
    await expect(
      dialog.getByRole("button", { name: /^Apply$/ }),
    ).toBeEnabled();

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).not.toBeVisible();
  });
});
