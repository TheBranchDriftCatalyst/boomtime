import { expect, test, type Page } from "@playwright/test";

// groupby-dnd — drag-to-reorder for the Group-by axis chips on the Books
// explorer (/app/books), the feature shipped in 7327829.
//
// The chips in <GroupByBar> are native HTML5 draggables: each chip
// (data-testid="groupby-chip-<i>") sets the source index into
// dataTransfer("text/plain") on dragstart, and the drop target reorders
// groupBy → the shared <GroupableExplorer> re-queries with the new PRIMARY
// axis. Row-number badges (1, 2, …) reflect the order.
//
// This spec drives that end to end against the deterministic reading seed
// (cmd/boomtime seed-reading-demo, global-setup seeds it for the e2e user):
//
//   1. add two axes (Genre, then Status) so the chips read "1 Genre … 2 Status"
//      and the grouped view buckets on the PRIMARY axis (Genre) — the six
//      seed genres show as top-level group rows;
//   2. drag chip 2 (Status) onto chip 1 (Genre) → assert the badges swap
//      (Status becomes 1, Genre becomes 2) AND the grouped view re-queried:
//      the genre buckets are gone and the status buckets (reading / want)
//      are now the top-level group rows;
//   3. a drop-on-self is a no-op (order unchanged, no re-query).
//
// Why the manual dispatch: Playwright's locator.dragTo() drives mouse
// move/down/up, which does NOT reliably fire the HTML5 drag lifecycle
// (dragstart/dragover/drop with a live DataTransfer) that these handlers
// depend on. So the drag is performed by dispatching real DragEvents in the
// page, threading ONE DataTransfer through the whole sequence so the source
// index written on dragstart is readable on drop — exactly matching
// GroupByBar's handlers.

// The reading seed's six PRIMARY genres (genres->>0) — the Genre-axis group
// rows. Mirrors books-explore.spec.ts's SEED_GENRES.
const SEED_GENRES = [
  "Science Fiction & Fantasy",
  "Literature & Fiction",
  "Romance",
  "Mystery Thriller & Suspense",
  "Business & Careers",
  "Teen & Young Adult",
];
// One genre used as an anchor for "grouped by genre right now".
const ANCHOR_GENRE = "Science Fiction & Fantasy";
// Raw status column values the seed carries ("read" | "reading" | "want").
// These are the Status-axis group rows once Status becomes the primary axis.
// "reading" and "want" are unambiguous exact-match labels.
const SEED_STATUSES = ["reading", "want"];

/** The single explorer table. */
function explorerTable(page: Page) {
  return page.locator("table").first();
}

const chips = (page: Page) =>
  page.locator('[data-testid^="groupby-chip-"]');

/**
 * Open the Group-by bar's Add-axis popover, pick an axis by label, confirm the
 * chip landed, then close the popover. The popover does NOT auto-close on
 * select (Radix closes only on outside-click / Escape), so a naive second
 * addAxis would click the trigger while the popover is still open and toggle it
 * shut. Pressing Escape leaves a clean, closed popover for the next add.
 */
async function addAxis(page: Page, axisLabel: string) {
  const before = await chips(page).count();
  await page.getByRole("button", { name: "Add axis" }).click();
  const item = page.getByRole("button", { name: axisLabel, exact: true });
  await expect(item).toBeVisible();
  await item.click();
  await expect(chips(page)).toHaveCount(before + 1);
  await page.keyboard.press("Escape");
  await expect(item).toBeHidden();
}

const chip = (page: Page, i: number) => page.getByTestId(`groupby-chip-${i}`);

/**
 * Perform a native HTML5 drag of chip `fromIndex` onto chip `toIndex` by
 * dispatching real DragEvents in the page. A single DataTransfer is threaded
 * through dragstart → dragover → drop so the source index written by
 * GroupByBar.onDragStart is present for GroupByBar.onDrop to read — the same
 * contract the browser's DnD provides. bubbles:true lets React's delegated
 * (root-attached) listeners receive each event.
 */
async function dragChip(page: Page, fromIndex: number, toIndex: number) {
  await page.evaluate(
    ({ fromIndex, toIndex }) => {
      const from = document.querySelector(
        `[data-testid="groupby-chip-${fromIndex}"]`,
      );
      const to = document.querySelector(
        `[data-testid="groupby-chip-${toIndex}"]`,
      );
      if (!from || !to) {
        throw new Error(
          `groupby chip(s) not found: ${fromIndex} -> ${toIndex}`,
        );
      }
      // ONE DataTransfer for the whole gesture — this is the crux: setData on
      // dragstart and getData on drop must see the same store.
      const dt = new DataTransfer();
      const box = (to as HTMLElement).getBoundingClientRect();
      const clientX = box.left + box.width / 2;
      const clientY = box.top + box.height / 2;
      const fire = (el: Element, type: string) => {
        const ev = new DragEvent(type, {
          bubbles: true,
          cancelable: true,
          composed: true,
          dataTransfer: dt,
          clientX,
          clientY,
        });
        el.dispatchEvent(ev);
      };
      fire(from, "dragstart");
      fire(to, "dragenter");
      fire(to, "dragover");
      fire(to, "drop");
      fire(from, "dragend");
    },
    { fromIndex, toIndex },
  );
}

test.beforeEach(async ({ page }) => {
  await page.goto("/app/books");
  await expect(page).toHaveURL(/\/app\/books/);
  await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
});

test("dragging chip 2 onto chip 1 reorders the axes and re-queries the primary grouping", async ({
  page,
}) => {
  const table = explorerTable(page);

  // --- Arrange: two axes, Genre PRIMARY then Status ------------------------
  await addAxis(page, "Genre");
  await addAxis(page, "Status");

  // Chips read "1 Genre" then "2 Status".
  await expect(chip(page, 0)).toContainText("1");
  await expect(chip(page, 0)).toContainText("Genre");
  await expect(chip(page, 1)).toContainText("2");
  await expect(chip(page, 1)).toContainText("Status");

  // Grouped-by-Genre right now: the six seed genres are the top-level group
  // rows (proves the view queried with Genre as the primary axis).
  for (const genre of SEED_GENRES) {
    await expect(table.getByText(genre, { exact: true })).toBeVisible();
  }
  // Status values are NOT top-level rows while Genre is primary.
  for (const status of SEED_STATUSES) {
    await expect(table.getByText(status, { exact: true })).toHaveCount(0);
  }

  // --- Act: drag chip 2 (Status, index 1) onto chip 1 (Genre, index 0) -----
  await dragChip(page, 1, 0);

  // --- Assert: badges swapped (Status→1, Genre→2) --------------------------
  await expect(chip(page, 0)).toContainText("1");
  await expect(chip(page, 0)).toContainText("Status");
  await expect(chip(page, 1)).toContainText("2");
  await expect(chip(page, 1)).toContainText("Genre");

  // --- Assert: the grouped view re-queried with Status as PRIMARY ----------
  // The genre buckets are gone; the status buckets are now the top-level rows.
  await expect(table.getByText(ANCHOR_GENRE, { exact: true })).toHaveCount(0);
  for (const status of SEED_STATUSES) {
    await expect(table.getByText(status, { exact: true })).toBeVisible();
  }
});

test("dropping a chip on itself is a no-op", async ({ page }) => {
  const table = explorerTable(page);

  await addAxis(page, "Genre");
  await addAxis(page, "Status");

  await expect(chip(page, 0)).toContainText("Genre");
  await expect(chip(page, 1)).toContainText("Status");
  // Genre is primary → genre buckets visible.
  await expect(table.getByText(ANCHOR_GENRE, { exact: true })).toBeVisible();

  // Drop chip 1 onto itself.
  await dragChip(page, 0, 0);

  // Order is unchanged and the primary grouping did NOT change.
  await expect(chip(page, 0)).toContainText("1");
  await expect(chip(page, 0)).toContainText("Genre");
  await expect(chip(page, 1)).toContainText("2");
  await expect(chip(page, 1)).toContainText("Status");
  await expect(table.getByText(ANCHOR_GENRE, { exact: true })).toBeVisible();
});
