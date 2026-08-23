import { expect, test, type Page } from "@playwright/test";

// books-explore — /app/books, the MERGED groupable Books view (boom-02sh Track C).
//
// The old "Explore" toggle + measure buttons + `explore-groups` testid were
// retired when the flat Table and the Explore breakdown collapsed onto ONE
// shared <GroupableExplorer> driven by the reading DomainConfig
// (booksExplorerConfig). This spec exercises that merged surface end to end:
//
//   (a) flat default (groupBy=[]) renders the book leaf rows,
//   (b) adding a group axis via the "Group by" bar renders nested group rows
//       with count + runtime rollups,
//   (c) expanding a group drills to its book leaf rows,
//   (d) flat-root pagination (exercised only when the seed exceeds one page),
//   (e) the server-side search box narrows the library.
//
// Data-driven against the deterministic reading seed (cmd/boomtime
// seed-reading-demo → 40 audible books across 6 primary genres, 3 series;
// 28 finished / 5 reading / 7 want). global-setup seeds it for the e2e user, so
// the assertions anchor on known seed content (titles, genres) rather than DOM
// internals. The explorer table carries no testids — rows are plain <tr>/<td> —
// so selectors are text/role based against the actual rendered chrome.

// The reading seed's six PRIMARY genres (genres->>0). Grouping by "genre"
// buckets on the first array element only (internal/query/domains.go), so these
// are exactly the group rows the Genre axis produces.
const SEED_GENRES = [
  "Science Fiction & Fantasy",
  "Literature & Fiction",
  "Romance",
  "Mystery Thriller & Suspense",
  "Business & Careers",
  "Teen & Young Adult",
];

// A couple of stable seed titles used as "is the library rendering?" anchors.
const SEED_TITLE_ANY = "Project Hail Mary"; // SFF, finished
const SEED_TITLE_BUSINESS = "Atomic Habits"; // Business & Careers, finished

/** The single explorer table (hero + controls carry no <table>). */
function explorerTable(page: Page) {
  return page.locator("table").first();
}

/** Open the "Group by" bar's Add-axis popover and pick an axis by label. */
async function addAxis(page: Page, axisLabel: string) {
  await page.getByRole("button", { name: "Add axis" }).click();
  // Popover items are <button>s labelled with the axis name. `exact` avoids
  // colliding with the "Source"/"Status" column headers + filter labels.
  await page.getByRole("button", { name: axisLabel, exact: true }).click();
}

test.beforeEach(async ({ page }) => {
  await page.goto("/app/books");
  await expect(page).toHaveURL(/\/app\/books/);
  // The merged explorer only renders when the books feature is enabled; the
  // table is the reliable "view is up" signal.
  await expect(explorerTable(page)).toBeVisible({ timeout: 15_000 });
});

test("flat default renders the book leaf rows", async ({ page }) => {
  const table = explorerTable(page);

  // The flat (zero-axis) root is a single leaf group seeded expanded, so its
  // book rows show immediately. The leaf-group header reports the total.
  await expect(table.getByText(/\d+ rows/)).toBeVisible();

  // Known seed titles render as leaf rows (the flat table lists the whole
  // library — 40 rows < the 250 page size — so both are on the one page).
  await expect(table.getByText(SEED_TITLE_ANY)).toBeVisible();
  await expect(table.getByText(SEED_TITLE_BUSINESS)).toBeVisible();

  // Leaf columns are present (the flat book table, not a group breakdown).
  await expect(
    table.getByRole("columnheader", { name: "Title" }),
  ).toBeVisible();
  await expect(
    table.getByRole("columnheader", { name: "Author" }),
  ).toBeVisible();
});

test("adding a group axis renders nested group rows with rollups", async ({
  page,
}) => {
  const table = explorerTable(page);

  await addAxis(page, "Genre");

  // The chosen axis shows as a chip in the Group-by bar.
  await expect(page.getByText("Group by:")).toBeVisible();

  // All six primary-genre group rows render.
  for (const genre of SEED_GENRES) {
    await expect(table.getByText(genre, { exact: true })).toBeVisible();
  }

  // Each group row carries its rollups: a count badge + a runtime figure.
  // Every seeded genre totals well over an hour of runtime, so "…h" always
  // appears; the count badge is the leading number on the row.
  const sffRow = table.locator("tr", {
    hasText: "Science Fiction & Fantasy",
  });
  await expect(sffRow).toContainText(/\d+h/); // runtime rollup
  await expect(sffRow.locator("text=/^\\d+$/").first()).toBeVisible(); // count badge

  // Grouping replaced the flat leaf table — the book leaf rows are no longer
  // top-level until a group is drilled.
  await expect(table.getByText(SEED_TITLE_BUSINESS)).toHaveCount(0);
});

test("expanding a group drills into its book leaf rows", async ({ page }) => {
  const table = explorerTable(page);

  await addAxis(page, "Genre");
  await expect(
    table.getByText("Business & Careers", { exact: true }),
  ).toBeVisible();

  // Expand the Business & Careers group → it reveals a child "Books" leaf
  // group (collapsed) …
  await table.getByText("Business & Careers", { exact: true }).click();
  const leafGroup = table.getByText("Books", { exact: true });
  await expect(leafGroup).toBeVisible();

  // … expand that leaf group → its book rows load, scoped to the genre.
  await leafGroup.click();
  await expect(table.getByText(SEED_TITLE_BUSINESS)).toBeVisible();
});

test("flat-root pagination advances when the library exceeds one page", async ({
  page,
}) => {
  const table = explorerTable(page);
  await expect(table.getByText(/\d+ rows/)).toBeVisible();

  // The flat root reuses the shared leaf pager, which only renders when the
  // total exceeds one page (leafPageSize = 250). The deterministic seed is 40
  // rows, so with this fixture there is a single page and no pager — assert
  // that reality. If a larger library is ever seeded the Next button appears;
  // exercise it then so this stays meaningful.
  const nextBtn = table.getByRole("button", { name: "Next" });
  if (await nextBtn.count()) {
    const pageIndicator = table.getByText(/Page \d+ \/ \d+/);
    await expect(pageIndicator).toContainText("Page 1 /");
    await nextBtn.first().click();
    await expect(pageIndicator).toContainText("Page 2 /");
  } else {
    // Single-page library: every row is on page one, no pager.
    await expect(table.getByText(SEED_TITLE_ANY)).toBeVisible();
    await expect(table.getByText(SEED_TITLE_BUSINESS)).toBeVisible();
  }
});

test("server-side search narrows the library", async ({ page }) => {
  const table = explorerTable(page);

  // Both a Sanderson title and a non-match are present in the full library.
  await expect(table.getByText("The Final Empire")).toBeVisible();
  await expect(table.getByText(SEED_TITLE_BUSINESS)).toBeVisible();

  // Search folds into the DSL `where` as an ILIKE on title OR author. "Sanderson"
  // matches the 3 Brandon Sanderson (Mistborn) rows and nothing else — the query
  // re-runs server-side (debounced) and the non-matching rows drop out.
  await page
    .getByPlaceholder(/Search title or author/)
    .fill("Sanderson");

  await expect(table.getByText("The Final Empire")).toBeVisible();
  await expect(table.getByText(SEED_TITLE_BUSINESS)).toHaveCount(0);
  // The leaf-group total reflects the narrowed set (server total, not a
  // client-side page filter): exactly the 3 Sanderson books.
  await expect(table.getByText(/3 rows/)).toBeVisible();
});
