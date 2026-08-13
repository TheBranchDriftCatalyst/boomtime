import { expect, test } from "@playwright/test";

// books-explore — /app/books → Explore mode → group-by a dimension → ranked
// groups render. Exercises the cross-domain query DSL surface (runQuery →
// POST /api/v1/query grouped over reading_items). Requires reading rows to be
// seeded for the e2e user (see the suite's stack-boot seed); an empty library
// would render the "nothing to break down" empty state instead of ranked bars.

test("Explore mode groups the library by a dimension into ranked bars", async ({
  page,
}) => {
  await page.goto("/app/books");
  await expect(page).toHaveURL(/\/app\/books/);

  // Toggle from the flat Table view into Explore (both are header buttons only
  // rendered when books_enabled is on).
  await page.getByRole("button", { name: "Explore" }).click();

  // Group by Genre (a dimension valid for the default Books-count measure).
  await page.getByRole("button", { name: "Genre" }).click();

  // Ranked groups render — the divided bar table with at least one group row.
  const groups = page.getByTestId("explore-groups");
  await expect(groups).toBeVisible({ timeout: 10_000 });
  await expect(groups.locator(":scope > div").first()).toBeVisible();

  // Switching the measure to Runtime re-runs the grouped query and still renders
  // ranked groups (genre is valid for runtime too).
  await page.getByRole("button", { name: "Runtime" }).click();
  await expect(groups).toBeVisible();
  await expect(groups.locator(":scope > div").first()).toBeVisible();
});
