import { expect, test } from "@playwright/test";
import {
  ADMIN_PASSWORD,
  ADMIN_USERNAME,
  NO_ADMIN_CREDS_REASON,
  NO_STACK_REASON,
  loginAsAdmin,
  stackReachableFromEnv,
} from "./helpers";

// gaka-vh8 + 6560211 — Backfill admin tab.
//
// Non-admins can't reach the tab at all (route-gated). That path is
// verified in admin-sidebar.spec.ts; here we test the admin surface:
//   * Stat cards + CLI hint present.
//   * ℹ tooltips render the expected explainer copy on hover.
//   * Live queue table has its connection indicator and (typically empty)
//     "no active runs" state.
//   * Danger zone: expandable accordion + DELETE-confirm interlock.

test.describe("gaka-vh8 — Backfill admin tab (admin only)", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);
  test.skip(!ADMIN_USERNAME || !ADMIN_PASSWORD, NO_ADMIN_CREDS_REASON);

  test.beforeEach(async ({ page }) => {
    const ok = await loginAsAdmin(page);
    test.skip(!ok, NO_ADMIN_CREDS_REASON);
    await page.goto("/app/admin/backfill");
    // The route mount is lazy — wait for the heading before probing.
    await expect(
      page.getByRole("heading", { name: /git-history backfill/i }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("renders the three stat cards", async ({ page }) => {
    // StatCards use label text as their headers ("Backfilled rows",
    // "Sources", "Range"). Assert all three appear.
    for (const label of ["Backfilled rows", "Sources", "Range"]) {
      await expect(page.getByText(label, { exact: true })).toBeVisible();
    }
  });

  test("config sliders + tooltip explanations render on hover", async ({
    page,
  }) => {
    // Each slider row has an aria-label on the label element. Use
    // getByText — the labels change to include the current minute value.
    for (const label of [
      /Cluster gap/i,
      /Pre-commit lead/i,
      /Post-commit tail/i,
      /Heartbeat rate/i,
    ]) {
      await expect(page.getByText(label).first()).toBeVisible();
    }

    // ℹ tooltip on Cluster gap: hover the info button next to the label
    // and assert the explainer copy renders. The tooltip button carries
    // aria-label="What is this?".
    const clusterRow = page
      .locator("div", { hasText: /Cluster gap/ })
      .first();
    const info = clusterRow
      .getByRole("button", { name: /what is this\?/i })
      .first();
    await info.hover();
    await expect(
      page.getByText(
        /How commits get grouped into a coding session/i,
      ),
    ).toBeVisible({ timeout: 5_000 });
  });

  test("author emails + source tag tooltips render", async ({ page }) => {
    // "Author emails" tooltip copy: "Which commits count as yours."
    const emailRow = page.locator("div", { hasText: /Author emails/i }).first();
    await emailRow
      .getByRole("button", { name: /what is this\?/i })
      .first()
      .hover();
    await expect(
      page.getByText(/Which commits count as yours/i),
    ).toBeVisible({ timeout: 5_000 });

    // Source tag tooltip copy: "How backfilled rows are labeled in the DB."
    const sourceRow = page.locator("div", { hasText: /Source tag/i }).first();
    await sourceRow
      .getByRole("button", { name: /what is this\?/i })
      .first()
      .hover();
    await expect(
      page.getByText(/How backfilled rows are labeled in the DB/i),
    ).toBeVisible({ timeout: 5_000 });
  });

  test("CLI command hint block is copyable", async ({ page }) => {
    // The CLIHint renders a <pre> containing the boomtime backfill
    // command. Verify presence + content.
    const pre = page.locator("pre", { hasText: /boomtime backfill git/i });
    await expect(pre).toBeVisible();
    await expect(pre).toContainText("--api");
    await expect(pre).toContainText("$BOOM_ADMIN_TOKEN");
  });

  test("live queue renders empty-state text or the connection indicator", async ({
    page,
  }) => {
    // In a fresh CI environment the queue is almost always empty. We
    // accept either the empty-state copy OR the WS connection status
    // pill — the goal is just to prove the widget mounted.
    const empty = page.getByText(
      /no active runs — start the CLI to see jobs land/i,
    );
    const live = page.getByText(/^live$/i);
    const reconnecting = page.getByText(/^reconnecting$/i);
    await expect(empty.or(live).or(reconnecting).first()).toBeVisible({
      timeout: 10_000,
    });
  });

  test("danger zone expands + confirm interlock disables the delete button", async ({
    page,
  }) => {
    // <details><summary>Danger zone</summary>…</details>. Click the
    // summary to expand.
    const summary = page.locator("summary", { hasText: /danger zone/i });
    await expect(summary).toBeVisible();
    await summary.click();

    // The confirm input placeholder text is `type "DELETE" to confirm`.
    const confirmInput = page.getByPlaceholder(/type "DELETE" to confirm/);
    await expect(confirmInput).toBeVisible({ timeout: 5_000 });

    const deleteBtn = page.getByRole("button", {
      name: /delete backfilled rows/i,
    });
    // Disabled while the input is empty / not "DELETE".
    await expect(deleteBtn).toBeDisabled();

    // Type the confirm string — button enables.
    await confirmInput.fill("DELETE");
    await expect(deleteBtn).toBeEnabled();

    // Clear the input again — button disables. IMPORTANT: do NOT click
    // the button. This test never actually deletes.
    await confirmInput.fill("");
    await expect(deleteBtn).toBeDisabled();
  });
});
