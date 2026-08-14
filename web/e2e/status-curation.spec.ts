import { expect, test, type Locator, type Page } from "@playwright/test";

// status-curation — the Hardcover status-curation OVERRIDE on the Books explorer
// (/app/books), shipped in 1b7e484 (gaka-books). The Status column is now an
// editable dropdown (StatusSelect) over the 5 canonical statuses
// (want|reading|read|paused|dnf). Changing it PATCHes
//   /api/v1/books/items/:externalId/curation?source=<kindle|audible>
// which writes status_override + curation_updated_at. The EFFECTIVE status
// (COALESCE(status_override, status), see internal/query/domains.go) is what
// drives the table, the Status group-by axis, the rollups, AND the Status
// filter. So overriding a 'reading' book to 'dnf' must make it LEAVE the
// 'reading' effective-status group and appear under a 'dnf' group.
//
// All Hardcover WRITES are dry-run-gated (BOOM_HARDCOVER_DRYRUN default true) so
// nothing hits real Hardcover — this spec verifies the LOCAL effective-status
// effect only (the persisted override changing grouping + filtering).
//
// Data-driven against the deterministic reading seed (cmd/boomtime
// seed-reading-demo): 41 audible books, 5 of them status='reading'. global-setup
// seeds it for the isolated e2e user. The explorer's grouped tree is NOT
// react-query backed (it runs on its own useExplorerTree cache — see
// useBookCuration's comment), so a curation write does NOT live-update the
// grouped tree; the persisted override is observed on the next server query,
// which we force with a page reload. That reload is the honest "re-query".

// The target reading book we override. Deterministic in the seed:
// DEMOASIN0016, source=audible, seeded status='reading', no subtitle (a clean
// row anchor). Its effective status must move reading → dnf after the override.
const TARGET_TITLE = "Gideon the Ninth";
const TARGET_ID = "DEMOASIN0016";
const TARGET_SOURCE = "audible";
// A different book that STAYS 'reading' — the control proving the override is
// scoped to one row, not a blanket status change.
const CONTROL_READING_TITLE = "Demon Copperhead";

// Effective-status GROUP row labels are the raw lowercase status value
// (COALESCE(status_override, status)); "reading" and "dnf" are unambiguous
// exact-match labels (mirrors groupby-dnd.spec.ts / books-explore.spec.ts).
const READING_GROUP = "reading";
const DNF_GROUP = "dnf";
// The leaf group the reading DomainConfig nests book rows under (booksExplorerConfig).
const LEAF = "Books";

/** The single explorer table (hero + controls carry no <table>). */
function explorerTable(page: Page): Locator {
  return page.locator("table").first();
}

/** The editable StatusSelect trigger inside a specific book's row. */
function statusTrigger(row: Locator): Locator {
  // aria-label is `Status: <Label>. Change status` (cells.tsx StatusSelect).
  return row.getByRole("button", { name: /^Status: .*Change status$/ });
}

/**
 * Add the "Status" group-by axis. Mirrors the robust addAxis from
 * groupby-dnd.spec.ts: the Radix popover does NOT auto-close on select, so
 * press Escape after to leave it clean for the next interaction.
 */
async function addStatusAxis(page: Page): Promise<void> {
  const chips = page.locator('[data-testid^="groupby-chip-"]');
  const before = await chips.count();
  await page.getByRole("button", { name: "Add axis" }).click();
  const item = page.getByRole("button", { name: "Status", exact: true });
  await expect(item).toBeVisible();
  await item.click();
  await expect(chips).toHaveCount(before + 1);
  await page.keyboard.press("Escape");
  await expect(item).toBeHidden();
}

/**
 * Drill an effective-status GROUP row down to its book leaf rows: click the
 * group value to expand it, then expand its nested "Books" leaf. Assumes only
 * ONE group is expanded at a time (the flat table renders leaf groups as
 * siblings, so two open "Books" rows would be ambiguous) — callers collapse the
 * previous group before drilling the next.
 */
async function drillGroupToBooks(page: Page, group: string): Promise<void> {
  const table = explorerTable(page);
  await table.getByText(group, { exact: true }).click();
  const leaf = table.getByText(LEAF, { exact: true });
  await expect(leaf).toBeVisible();
  await leaf.click();
}

/** Collapse an expanded group row (toggles it shut, removing its subtree). */
async function collapseGroup(page: Page, group: string): Promise<void> {
  await explorerTable(page).getByText(group, { exact: true }).click();
}

/**
 * Reset the target book's curation override back to effective 'reading' via the
 * API, so every test (and every --repeat-each iteration) starts from the same
 * seed state — a prior run's persisted dnf override would otherwise break the
 * "Gideon starts under reading" precondition. Uses a fresh access token minted
 * from the storageState refresh cookie.
 */
async function resetTargetToReading(page: Page): Promise<void> {
  const refreshed = await page.request.post("/auth/refresh_token");
  expect(refreshed.ok(), "refresh_token to mint access token").toBeTruthy();
  const token = (await refreshed.json()).token as string;
  const res = await page.request.patch(
    `/api/v1/books/items/${TARGET_ID}/curation?source=${TARGET_SOURCE}`,
    {
      headers: { Authorization: `Basic ${token}` },
      data: { status: "reading" },
    },
  );
  expect(res.ok(), "reset target override to reading").toBeTruthy();
}

test.beforeEach(async ({ page }) => {
  // Hermetic reset BEFORE the first load so the initial query sees the target
  // under its seeded 'reading' effective status regardless of prior runs.
  await page.goto("/app/books");
  await resetTargetToReading(page);
  await page.reload();
  await expect(page).toHaveURL(/\/app\/books/);
  await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
});

test("overriding a reading book to DNF moves it between effective-status groups", async ({
  page,
}) => {
  const table = explorerTable(page);

  // --- Arrange: group by effective Status. The 'reading' bucket exists (the
  // seed has 5 reading books); no 'dnf' bucket exists yet (seed has none). ----
  await addStatusAxis(page);
  await expect(table.getByText(READING_GROUP, { exact: true })).toBeVisible();
  await expect(table.getByText(DNF_GROUP, { exact: true })).toHaveCount(0);

  // Drill the reading group to its book rows and confirm the target is there.
  await drillGroupToBooks(page, READING_GROUP);
  const targetRow = table.locator("tr", { hasText: TARGET_TITLE });
  await expect(targetRow).toBeVisible();

  // --- Act: open the target's Status dropdown and pick DNF. This fires the
  // curation PATCH; wait for it to land so the override is persisted. ---------
  await statusTrigger(targetRow).click();
  const dnfOption = page.getByRole("menuitem", { name: "DNF", exact: true });
  await expect(dnfOption).toBeVisible();

  const patch = page.waitForResponse(
    (r) => r.url().includes("/curation") && r.request().method() === "PATCH",
  );
  await dnfOption.click();
  const patchResp = await patch;
  expect(patchResp.status(), "curation PATCH 200").toBe(200);

  // Optimistic cell flip: the pill immediately reads DNF on that row.
  await expect(statusTrigger(targetRow)).toHaveAttribute(
    "aria-label",
    /^Status: DNF\./,
  );

  // --- Re-query: the grouped tree is not react-query backed, so reload to pull
  // the persisted effective status from the server, then regroup by Status. ---
  await page.reload();
  await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
  await addStatusAxis(page);

  // --- Assert: a 'dnf' effective-status group now EXISTS (it did not before) -
  await expect(table.getByText(DNF_GROUP, { exact: true })).toBeVisible();
  await expect(table.getByText(READING_GROUP, { exact: true })).toBeVisible();

  // The target now lives UNDER dnf.
  await drillGroupToBooks(page, DNF_GROUP);
  await expect(table.locator("tr", { hasText: TARGET_TITLE })).toBeVisible();
  await collapseGroup(page, DNF_GROUP);
  await expect(table.getByText(LEAF, { exact: true })).toHaveCount(0);

  // …and is GONE from the reading group, while a still-reading control remains.
  await drillGroupToBooks(page, READING_GROUP);
  await expect(
    table.locator("tr", { hasText: CONTROL_READING_TITLE }),
  ).toBeVisible();
  await expect(table.locator("tr", { hasText: TARGET_TITLE })).toHaveCount(0);
});

test("the Status filter offers the canonical set and filters by effective (overridden) status", async ({
  page,
}) => {
  const table = explorerTable(page);

  // The page Status FILTER is a native <select> in a <label> whose span reads
  // "Status" (BooksPage FilterSelect).
  const statusFilter = page
    .locator("label")
    .filter({ has: page.getByText("Status", { exact: true }) })
    .locator("select");

  // --- Assert the disconnect fix: the filter speaks the ONE canonical
  // vocabulary (want/reading/read/paused/dnf) — same values as the group-by
  // axis + the pill keys — with an "all" lead. Was the mismatched
  // all|reading|finished|want before gaka-books. ----------------------------
  const values = await statusFilter.evaluate((el) =>
    Array.from((el as HTMLSelectElement).options).map((o) => o.value),
  );
  expect(values).toEqual(["all", "want", "reading", "read", "paused", "dnf"]);
  await expect(statusFilter.locator("option")).toHaveText([
    "All",
    "Want",
    "Reading",
    "Finished",
    "Paused",
    "DNF",
  ]);

  // Sanity: with no filter the target is present and reading; a 'dnf' filter
  // currently yields nothing (seed has no dnf books).
  await expect(table.getByText(TARGET_TITLE)).toBeVisible();

  // --- Override the target reading → dnf via the editable Status cell. -------
  const targetRow = table.locator("tr", { hasText: TARGET_TITLE });
  await statusTrigger(targetRow).click();
  const dnfOption = page.getByRole("menuitem", { name: "DNF", exact: true });
  await expect(dnfOption).toBeVisible();
  const patch = page.waitForResponse(
    (r) => r.url().includes("/curation") && r.request().method() === "PATCH",
  );
  await dnfOption.click();
  expect((await patch).status()).toBe(200);

  // --- Filter to 'dnf'. Changing the filter re-queries the explorer server-
  // side (the where predicate folds onto the EFFECTIVE status), so the
  // overridden book now matches and reading-only books drop out. -------------
  await statusFilter.selectOption("dnf");
  await expect(table.getByText(TARGET_TITLE)).toBeVisible();
  await expect(table.getByText(CONTROL_READING_TITLE)).toHaveCount(0);
  // The flat leaf-group total reflects exactly the one overridden book.
  await expect(table.getByText(/^1 rows/)).toBeVisible();
});
