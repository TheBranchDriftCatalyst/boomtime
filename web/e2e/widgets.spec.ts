import { expect, test } from "@playwright/test";

/**
 * gaka-hsj — embeddable widgets happy path against the live dev stack.
 * Auth comes from the storageState captured in global-setup (same as
 * add-to-space.spec.ts); heartbeats are seeded there too.
 */
test("widgets panel opens, previews render, public SVG URL serves", async ({
  page,
}) => {
  await page.goto("/app");
  await expect(page).toHaveURL(/\/app(\/|$)/);

  // Open the Widgets panel from the Overview toolbar.
  await page.getByRole("button", { name: "Open widgets panel" }).click();
  await expect(page.getByText("Embeddable widgets")).toBeVisible();

  // The link mints lazily on open; the catalog renders one card per user-
  // scope entry, each preview an <object type="image/svg+xml"> (not <img>) so
  // the SVG's native <title> tooltips and hover styles work.
  const preview = page.getByLabel("Stats Card preview");
  await expect(preview).toBeVisible({ timeout: 10_000 });

  // Grade card is user-scope: present on Overview.
  await expect(page.getByText("Stats Card + Grade")).toBeVisible();

  // Fetch the public URL directly (no auth needed — the endpoint is public).
  const src = await preview.getAttribute("data");
  expect(src).toBeTruthy();
  const res = await page.request.get(src!);
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toContain("image/svg+xml");
  expect(await res.text()).toContain("<svg");
});

test("per-chart hover embed-link icons appear on mapped charts", async ({
  page,
}) => {
  await page.goto("/app");
  await expect(page).toHaveURL(/\/app(\/|$)/);

  // The Project breakdown card maps to the top-projects widget kind and
  // carries the hover-revealed live embed-link button.
  const projectCard = page
    .locator("[data-chart-card]", { hasText: "Project breakdown" })
    .first();
  await expect(projectCard).toBeVisible({ timeout: 15_000 });
  await projectCard.scrollIntoViewIfNeeded();
  await projectCard.hover();
  await expect(
    projectCard.getByRole("button", { name: "Copy embed link" }),
  ).toBeVisible();
});

test("settings has horizontal tabs incl. widgets, changelog and logs", async ({
  page,
}) => {
  await page.goto("/app/settings");
  const tablist = page.getByRole("tablist", { name: "Settings sections" });
  await expect(tablist).toBeVisible();
  for (const label of ["Hidden data", "Remappings", "Widgets", "Changelog", "Logs"]) {
    await expect(tablist.getByRole("tab", { name: label })).toBeVisible();
  }

  // Widgets tab shows the link list card.
  await tablist.getByRole("tab", { name: "Widgets" }).click();
  await expect(page.getByText("Widget links")).toBeVisible();

  // Old routes redirect into their tabs.
  await page.goto("/app/logs");
  await expect(page).toHaveURL(/\/app\/settings\?tab=logs/);
  await page.goto("/app/changelog");
  await expect(page).toHaveURL(/\/app\/settings\?tab=changelog/);
});
