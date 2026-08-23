import { expect, test } from "@playwright/test";

/**
 * boom-hsj — embeddable widgets happy path against the live dev stack.
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

test("settings rail lists the registered tabs incl. widgets and changelog", async ({
  page,
}) => {
  await page.goto("/app/settings");
  // boom-4x33: Settings' nav is a vertical rail now. Its entries switch on
  // ?tab= rather than routing, so they are buttons, not links.
  const rail = page.getByRole("navigation", { name: "Settings sections" });
  await expect(rail).toBeVisible();
  // "Logs" is deliberately absent: it moved to /app/admin/logs (boom-ebq) and
  // admin-sidebar.spec.ts asserts Settings no longer surfaces it. This list
  // still named it, so the expectation contradicted that spec.
  for (const label of ["Hidden data", "Remappings", "Widgets", "Changelog"]) {
    await expect(rail.getByRole("button", { name: label })).toBeVisible();
  }

  // Widgets tab shows the link list card.
  await rail.getByRole("button", { name: "Widgets" }).click();
  // Heading, not getByText: the empty-state copy below the card also contains
  // "widget links", so the loose text query is a strict-mode violation.
  await expect(
    page.getByRole("heading", { name: "Widget links" }),
  ).toBeVisible();

  // The legacy /app/logs redirect used to be asserted here as landing on
  // /app/settings?tab=logs. Logs moved to /app/admin/logs (boom-ebq), so that
  // expectation had been wrong ever since — and it can't live here anyway now:
  // this spec runs as the non-admin e2e user, for whom AdminRoute correctly
  // bounces /app/admin/* back to /app. admin-sidebar.spec.ts owns the legacy
  // redirect assertions, signed in as an admin, where they can actually pass.
  await page.goto("/app/changelog");
  await expect(page).toHaveURL(/\/app\/changelog/);
});
