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
// Verifies (flag OFF only):
//   - the menu offers NO Liberate action
//   - the detail sheet carries NO Liberation panel (the SPA-fallback trap above
//     is exactly what would light it up on an install that has no liberation)
// Verifies (flag ON only, pinned to a known-Audible row via ?source=audible):
//   - the menu offers EXACTLY ONE Liberate action
//   - the detail sheet carries the Liberation panel with its Liberate button

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

/**
 * Land on /app/books scoped to AUDIBLE rows only, so the flag-on assertions can
 * be exact instead of "0 or 1 is fine".
 *
 * Liberation is Audible-only — a Kindle ebook has no audiobook — so the old
 * `toBeLessThanOrEqual(1)` hedge existed purely because the first row's source
 * was unknown. The page seeds its Source filter from ?source (BooksPage reads
 * the query string on mount), and `boomtime seed-reading-demo` writes every
 * fixture row with source='audible' (cmd/boomtime/seed_reading.go), so pinning
 * the filter removes the ambiguity and lets the count be asserted as exactly 1.
 *
 * Returns false when the filtered table has no rows (an environment whose books
 * data never got seeded), so the caller can skip rather than fail.
 */
async function gotoAudibleOnly(page: Page): Promise<boolean> {
  await page.goto("/app/books?source=audible");
  await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
  const rows = explorerTable(page).locator("tbody tr");
  // The table shell paints before the filtered query resolves, so count() alone
  // would read 0 on a perfectly good library. Wait for a row, and treat the
  // timeout (not an exception) as "this environment has no seeded books".
  await rows
    .first()
    .waitFor({ state: "visible", timeout: 10_000 })
    .catch(() => {});
  if ((await rows.count()) === 0) return false;
  // Confirm the filter really took — every visible row badges as Audible. If the
  // ?source seeding ever regressed, this fails loudly here rather than turning
  // the assertions below back into a coin flip.
  await expect(rows.first().getByText("Audible")).toBeVisible({ timeout: 5_000 });
  return true;
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

  test("the menu does NOT offer Liberate when the feature is off", async ({ page }) => {
    test.skip(await liberationEnabled(page), "liberation is enabled here");

    await openRowMenu(page);
    // A visible button that 405s on click is worse than no button. This is the
    // assertion the availability probe has to keep true: with the feature off,
    // GET /api/v1/books/liberation/status answers 200 text/html from the SPA
    // catch-all, and treating that as "the feature is on" lights the whole
    // liberation surface up on an install that has none of it.
    await expect(
      contextMenu(page).getByRole("menuitem", { name: /Liberate/ }),
    ).toHaveCount(0);
  });

  test("the menu offers Liberate on an Audible row when the feature is on", async ({
    page,
  }) => {
    test.skip(!(await liberationEnabled(page)), "liberation is off here");
    test.skip(!(await gotoAudibleOnly(page)), "no seeded Audible titles");

    await openRowMenu(page);
    // EXACTLY one — the row is known-Audible, so 0 is a regression (a removed
    // menu item, a broken canLiberate), not an acceptable "Kindle row" outcome.
    await expect(
      contextMenu(page).getByRole("menuitem", { name: /Liberate/ }),
    ).toHaveCount(1);
  });

  test("the detail sheet carries the Liberation panel when the feature is on", async ({
    page,
  }) => {
    test.skip(!(await liberationEnabled(page)), "liberation is off here");
    test.skip(!(await gotoAudibleOnly(page)), "no seeded Audible titles");

    await openRowMenu(page);
    await contextMenu(page).getByRole("menuitem", { name: "Open details" }).click();

    const sheet = page.getByRole("dialog");
    await expect(sheet).toBeVisible({ timeout: 10_000 });

    // The sheet is keyed on the WORK, not the clicked row: it renders every
    // provider edition and the panel follows editions[0] (the headline). On the
    // seeded library that is the Audible row we clicked, but a real library can
    // put a Kindle edition first — so assert the actual contract (panel iff the
    // headline edition is Audible) rather than skipping. Both branches assert.
    const headSource = (
      await sheet.getByText(/^(Audible|Kindle|Hardcover)$/).first().innerText()
    ).trim();
    const panel = sheet.getByText("Liberation", { exact: true });

    if (headSource === "Audible") {
      // The panel's own section header, plus its primary action. Asserting only
      // that the dialog opened (as the "Open details" test does) would pass with
      // the liberation section entirely missing.
      await expect(panel).toBeVisible();
      await expect(
        sheet.getByRole("button", { name: /^(Liberate|Re-liberate|In progress…)$/ }),
      ).toBeVisible();
    } else {
      // Liberation is Audible-only — there is no audiobook behind a Kindle or
      // Hardcover headline edition, so the panel must stay absent.
      await expect(panel).toHaveCount(0);
    }
  });

  test("the detail sheet has NO Liberation panel when the feature is off", async ({
    page,
  }) => {
    test.skip(await liberationEnabled(page), "liberation is enabled here");

    await openRowMenu(page);
    await contextMenu(page).getByRole("menuitem", { name: "Open details" }).click();

    const sheet = page.getByRole("dialog");
    await expect(sheet).toBeVisible({ timeout: 10_000 });
    // Give the availability probe time to resolve — the failure mode is that it
    // resolves SUCCESSFULLY with the SPA index, so an immediate check would pass
    // for the wrong reason.
    await page.waitForTimeout(1_000);
    await expect(sheet.getByText("Liberation", { exact: true })).toHaveCount(0);
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
