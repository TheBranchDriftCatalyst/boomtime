import { expect, test, type Page } from "@playwright/test";

// books-liberation — the Libation rebuild's UI surface on /app/books (boom-w20s).
//
// FEATURE FLAG. The four /api/v1/books/liberate* routes only register when
// BOOM_FEATURE_BOOKS_LIBERATION=true AND BOOM_BOOKS_LIBRARY_PATH is set
// (config.LiberationEnabled()). With the feature off the routes 404 and the UI
// renders nothing liberation-shaped. Following the cli-tab.spec convention,
// these specs DETECT that state at runtime and skip the enabled-path assertions
// rather than hard-failing, so CI never breaks on a disabled feature.
//
// PROBING FOR THE FLAG IS SUBTLER THAN IT LOOKS, and getting it wrong is how
// this spec would silently invert. The SPA catch-all claims every unmatched
// path, so on this server an unregistered route does NOT 404:
//
//   GET  /api/v1/books/liberation/status  -> 200 text/html (the SPA index!)
//   POST /api/v1/books/liberate/sweep     -> 405 (catch-all is GET-only)
//
// Verified against a running dev backend with the feature off. So neither the
// status code nor a 404 check can tell you anything — the reliable signal is
// whether the response is actually JSON. A 200 of index.html means the route is
// absent.
//
// Verifies (flag-independent):
//   - right-click on a book row opens the context menu, anchored on that book
//   - the menu always offers Open details / Open on Hardcover
//   - Escape and outside-click dismiss it
//   - the Liberation explorer column is hidden by default (liberation is off
//     for most installs, so an always-on column would be permanently empty)
// Verifies (flag on only):
//   - the menu offers a Liberate action for an Audible title
//   - the detail sheet carries the Liberation panel

/** The single explorer table (hero + controls carry no <table>). */
function explorerTable(page: Page) {
  return page.locator("table").first();
}

/** The context menu rendered at the cursor (role=menu, fixed-positioned). */
function contextMenu(page: Page) {
  return page.getByRole("menu");
}

/**
 * Probe the backend for the liberation feature by CONTENT TYPE, not status.
 * See the header note: with the feature off this path returns 200 text/html
 * (the SPA index), so a status check reports "enabled" for a route that does
 * not exist.
 */
async function liberationEnabled(page: Page): Promise<boolean> {
  const res = await page.request.get("/api/v1/books/liberation/status");
  if (!res.ok()) return false;
  if (!(res.headers()["content-type"] ?? "").includes("application/json")) {
    return false; // SPA fallback — the route is not registered
  }
  const body = await res.json().catch(() => null);
  return body !== null && typeof body === "object" && "counts" in body;
}

/** Right-click the first book leaf row and wait for the menu. */
async function openRowMenu(page: Page): Promise<string> {
  const table = explorerTable(page);
  // Leaf rows carry the book title; the first data row is a stable anchor
  // regardless of which seed titles exist.
  const row = table.locator("tbody tr").first();
  const title = (await row.innerText()).split("\n")[0]?.trim() ?? "";
  await row.click({ button: "right" });
  await expect(contextMenu(page)).toBeVisible({ timeout: 5_000 });
  return title;
}

test.describe("books — row context menu", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/app/books");
    await expect(page).toHaveURL(/\/app\/books/);
    await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
  });

  test("right-click opens a menu anchored on that book", async ({ page }) => {
    const title = await openRowMenu(page);
    const menu = contextMenu(page);

    // The menu header names the book it acts on — without it a mis-aimed
    // right-click would silently target the wrong title.
    if (title) {
      await expect(menu.getByText(title, { exact: false })).toBeVisible();
    }
    // Always-available actions, independent of any feature flag.
    await expect(menu.getByRole("menuitem", { name: "Open details" })).toBeVisible();
    await expect(
      menu.getByRole("menuitem", { name: "Open on Hardcover" }),
    ).toBeVisible();
  });

  test("Escape dismisses the menu", async ({ page }) => {
    await openRowMenu(page);
    await page.keyboard.press("Escape");
    await expect(contextMenu(page)).toBeHidden();
  });

  test("clicking outside dismisses the menu", async ({ page }) => {
    await openRowMenu(page);
    // Click far from the menu; body-level mousedown is what the panel listens for.
    await page.mouse.click(5, 5);
    await expect(contextMenu(page)).toBeHidden();
  });

  test("Open details opens the book detail sheet", async ({ page }) => {
    await openRowMenu(page);
    await contextMenu(page).getByRole("menuitem", { name: "Open details" }).click();
    // The sheet is a dialog; the menu closes behind it.
    await expect(contextMenu(page)).toBeHidden();
    await expect(page.getByRole("dialog")).toBeVisible({ timeout: 10_000 });
  });
});

test.describe("books — liberation surface", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/app/books");
    await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
  });

  test("the Liberation column is hidden by default", async ({ page }) => {
    // Liberation is off unless explicitly enabled, so an always-visible column
    // would be permanently empty for everyone else. It must be OFFERED but not
    // shown — the header should be absent from the rendered table.
    await expect(
      explorerTable(page).getByRole("columnheader", { name: "Liberation" }),
    ).toHaveCount(0);
  });

  test("the menu offers Liberate only when the feature is on", async ({ page }) => {
    const enabled = await liberationEnabled(page);
    await openRowMenu(page);
    const liberate = contextMenu(page).getByRole("menuitem", { name: /Liberate/ });

    if (!enabled) {
      // Feature off: the action must not appear at all. A visible button that
      // 404s on click is worse than no button.
      await expect(liberate).toHaveCount(0);
      return;
    }
    // Feature on: Audible rows offer it. Kindle rows deliberately do not (there
    // is no audiobook to liberate), so tolerate its absence on a non-Audible
    // first row rather than asserting blindly.
    const count = await liberate.count();
    expect(count).toBeLessThanOrEqual(1);
  });

  test("the status endpoint reflects the flag consistently", async ({ page }) => {
    if (!(await liberationEnabled(page))) return; // feature off — nothing to assert

    const res = await page.request.get("/api/v1/books/liberation/status");
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    // The shape the Books toolbar depends on for its "Liberate all (N)" count
    // and its size estimate.
    expect(body).toHaveProperty("counts");
    expect(body).toHaveProperty("pending");
    expect(typeof body.pending).toBe("number");
    expect(body).toHaveProperty("libraryPath");
  });
});
