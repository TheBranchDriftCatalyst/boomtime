import { expect, test, type Page } from "@playwright/test";

// books-standalone — end-to-end proof that the STANDALONE catalyst-books app
// (cmd/catalyst-books + `yarn build:books`) runs FULLY in isolation (gaka-zp2s).
//
// Unlike the other specs in this suite (which drive the host stack at :5173 with
// a seeded, authenticated user), this one targets a running STANDALONE books
// server: a books-only DB, one fixed owner, NO auth. It asserts the isolation
// invariants the unit + integration layers can only check in pieces, but from
// the real browser against the real embedded SPA bundle:
//
//   - the shell brands as "CatalystBooks" (not "Boomtime");
//   - the nav is Books / Settings / Profile ONLY — no Overview, no Spaces, and
//     no boomtime code-domain entries (Projects / Leaderboards / Heartbeats / …);
//   - /app lands on the Books library (the standalone index redirect);
//   - the library table renders (the books surface actually works);
//   - a boomtime route (/app/projects) does NOT render a boomtime page — it is
//     unregistered in the books composition, so it redirects/blanks rather than
//     showing Projects.
//
// ── HOW TO RUN ──────────────────────────────────────────────────────────────
// The standalone server must be UP and reachable at $BOOKS_STANDALONE_URL
// (default http://localhost:18080). Bring it up, e.g.:
//
//     # build the books-only SPA into web/dist-books, then the lean binary:
//     (cd web && yarn build:books)
//     go build -o /tmp/catalyst-books ./cmd/catalyst-books
//     BOOM_ENV=dev BOOM_PORT=18080 \
//       BOOM_DB_HOST=<lan-ip> BOOM_DB_PASS=<pw> BOOM_STANDALONE_OWNER=owner \
//       /tmp/catalyst-books
//
// Then run ONLY this spec (it needs no host stack / seeded auth). The shared
// global-setup seeds the HOST stack, so bypass it for a pure-standalone run:
//
//     cd web && npx playwright test e2e/books-standalone.spec.ts \
//       --global-setup= --global-teardown=
//
// Or point the whole suite at the standalone by exporting BOOKS_STANDALONE_URL.
// (Author-only per the task — this spec is not executed in CI here yet.)

const STANDALONE_URL =
  process.env.BOOKS_STANDALONE_URL || "http://localhost:18080";

// Target the standalone server, and start from a CLEAN context: the standalone
// has no auth, so we must NOT reuse the host suite's captured storageState.
test.use({
  baseURL: STANDALONE_URL,
  storageState: { cookies: [], origins: [] },
});

/** The single library table on the Books view. */
function libraryTable(page: Page) {
  return page.locator("table").first();
}

/** Accessible names of the primary sidebar nav entries (role=link). */
async function navLinkNames(page: Page): Promise<string[]> {
  const links = page.getByRole("navigation").getByRole("link");
  return (await links.allInnerTexts()).map((t) => t.trim()).filter(Boolean);
}

test("standalone shell brands as CatalystBooks", async ({ page }) => {
  await page.goto("/app");
  // The standalone build folds VITE_BOOKS_STANDALONE=true, so the sidebar logo
  // title renders STANDALONE_APP_NAME instead of "Boomtime".
  await expect(page.getByText("CatalystBooks").first()).toBeVisible({
    timeout: 15_000,
  });
  await expect(page.getByText("Boomtime")).toHaveCount(0);
});

test("/app lands on the Books library (standalone index redirect)", async ({
  page,
}) => {
  await page.goto("/app");
  // The standalone index route is <Navigate to="/app/books" replace />.
  await expect(page).toHaveURL(/\/app\/books/);
  await expect(libraryTable(page)).toBeVisible({ timeout: 15_000 });
});

test("nav is Books / Settings / Profile only — no boomtime domains", async ({
  page,
}) => {
  await page.goto("/app/books");
  await expect(libraryTable(page)).toBeVisible({ timeout: 15_000 });

  // Books + Profile are always visible; Settings lives in the nav too.
  await expect(
    page.getByRole("link", { name: "Books", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Profile", exact: true }),
  ).toBeVisible();

  // The negative is the load-bearing part: NONE of the boomtime code-domain nav
  // entries (nor Overview / Spaces) may appear. If registerBooksAppDomains ever
  // pulled in registerBoomtimeDomain, one of these would surface and fail.
  const forbidden = [
    "Overview",
    "Spaces",
    "Projects",
    "Leaderboards",
    "Heartbeats",
    "Wellness",
    "Goals",
    "Import",
  ];
  const names = (await navLinkNames(page)).join("\n");
  for (const gone of forbidden) {
    expect(names, `boomtime nav entry "${gone}" leaked into the standalone`).not.toContain(
      gone,
    );
  }
});

test("a boomtime route does NOT render a boomtime page", async ({ page }) => {
  // /app/projects is unregistered in the books composition. It must NOT render a
  // Projects page — it redirects (catch-all → "/" → /app/books) or blanks.
  await page.goto("/app/projects");

  // Not parked on a rendered /app/projects Projects view …
  await expect(
    page.getByRole("heading", { name: /projects/i }),
  ).toHaveCount(0);
  // … and the books shell is still what's on screen (the app didn't 500/blank
  // into a boomtime surface). Either the library is showing or we bounced to it.
  await expect(
    page.getByRole("link", { name: "Books", exact: true }),
  ).toBeVisible({ timeout: 15_000 });
});
