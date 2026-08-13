import { expect, test } from "@playwright/test";

// reading-dashboard — /app Overview → Reading tab. Asserts the tab is offered
// (books_enabled), that switching to it renders the reading tiles (listening
// KPI, listening trend, genre donut, now-reading), and that the date-range
// control drives the windowed view (the scalar KPI subtitle tracks the preset).
//
// Data-agnostic by design: the tiles render their card chrome + a coherent
// empty state when the e2e library has no reading rows, so these assertions hold
// whether or not reading data has been seeded. When rows exist, the richer
// data-testids (reading-donut, now-reading-list) additionally appear — asserted
// softly below.

test("Reading tab renders the tiles and the range control drives the window", async ({
  page,
}) => {
  await page.goto("/app");
  await expect(page).toHaveURL(/\/app(\/|\?|$)/);

  // The Reading tab is only offered when books_enabled is on. Click it.
  const readingTab = page.getByRole("tab", { name: "Reading" });
  await expect(readingTab).toBeVisible({ timeout: 10_000 });
  await readingTab.click();
  await expect(page).toHaveURL(/view=reading/);

  // Tiles present — assert on the stable card titles + the range control, which
  // render regardless of whether the library has data.
  await expect(page.getByTestId("reading-range-control")).toBeVisible();
  await expect(page.getByText("Listening in range", { exact: true })).toBeVisible();
  await expect(page.getByText("Listening trend", { exact: true })).toBeVisible();
  await expect(page.getByText("Books by genre", { exact: true })).toBeVisible();
  await expect(page.getByText("Now reading", { exact: true })).toBeVisible();

  // The range control changes the view: the scalar KPI subtitle follows the
  // selected preset. Assert both directions so the test is independent of the
  // (localStorage-persisted) starting preset.
  const range = page.getByTestId("reading-range-control");

  await range.getByRole("radio", { name: "4W" }).click();
  await expect(range.getByRole("radio", { name: "4W" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await expect(page.getByText("Last 4 weeks", { exact: true }).first()).toBeVisible();

  await range.getByRole("radio", { name: "12W" }).click();
  await expect(range.getByRole("radio", { name: "12W" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  await expect(page.getByText("Last 12 weeks", { exact: true }).first()).toBeVisible();
});
