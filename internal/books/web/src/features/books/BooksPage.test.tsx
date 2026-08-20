// BooksPage.test.tsx — the merged groupable Books view (gaka-02sh Track C).
// runQuery is mocked (discriminated on the spec) so the real page pipeline runs:
//   - groupBy [] → the flat leaf table (DSL rows mode) renders book rows;
//   - adding an axis (Author) → the DSL grouped query renders group rows with
//     the count + runtime + finished rollups;
//   - a leaf row's trailing action opens that book's Hardcover page.
// The public config (books_enabled) is stubbed via MSW like the rest of the app.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import type { QuerySpec } from "@shared/lib/queryApi";
import { BooksPage } from "./BooksPage";

const { runQueryMock } = vi.hoisted(() => ({ runQueryMock: vi.fn() }));
vi.mock("@shared/lib/queryApi", () => ({ runQuery: runQueryMock }));

const row = (p: Record<string, unknown>) => ({
  source: "audible",
  externalId: "B1",
  title: "Untitled",
  authors: "",
  status: "reading",
  progressPercent: 0,
  finished: false,
  syncedAt: "2026-08-01T00:00:00Z",
  ...p,
});

// One runQuery impl for the whole page: rows mode → leaf rows; group "author" →
// ranked author groups (with rollups); source grouping → the hero breakdown.
// The source-grouped hero runs twice: unfiltered (no where) → whole-library
// totals; filter-scoped (where present) → a reduced slice, so the hero can
// render `<filtered>/<total>`.
function wireQueries() {
  runQueryMock.mockImplementation(async (spec: QuerySpec) => {
    // Reading Events tab (gaka-z5dz): the readingEvents domain. Rows mode → event
    // rows (one per read); grouped → per-origin read counts. Harmless to the
    // library tests, which never touch this domain.
    if (spec.domain === "readingEvents") {
      if (spec.rows) {
        return {
          kind: "rows",
          total: 1,
          rows: [
            {
              origin: "audible",
              source: "audible",
              externalId: "B08GB58KD5",
              title: "Project Hail Mary",
              authors: "Andy Weir",
              status: "read",
              finishedAt: "2026-08-10T00:00:00Z",
            },
          ],
        };
      }
      return {
        kind: "groups",
        groups: [{ key: "audible", value: 2, count: 2, stats: { count: 2 } }],
      };
    }
    if (spec.rows) {
      return {
        kind: "rows",
        total: 2,
        rows: [
          row({
            title: "Project Hail Mary",
            authors: "Andy Weir",
            externalId: "B08GB58KD5",
            progressPercent: 30,
          }),
          row({ title: "Dune", authors: "Frank Herbert", externalId: "B09" }),
        ],
      };
    }
    if (spec.group === "author") {
      return {
        kind: "groups",
        groups: [
          {
            key: "Brandon Sanderson",
            value: 12,
            count: 12,
            stats: { count: 12, runtime: 1320, finished: 5 },
          },
        ],
      };
    }
    // source grouping (the hero). A where means the FILTER-scoped hero → return
    // a reduced slice so filtered != total; otherwise the whole-library totals.
    if (spec.where != null) {
      return {
        kind: "groups",
        groups: [
          { key: "audible", value: 3, count: 3, stats: { count: 3, finished: 2 } },
        ],
      };
    }
    return {
      kind: "groups",
      groups: [
        { key: "audible", value: 8, count: 8, stats: { count: 8, finished: 4 } },
        { key: "kindle", value: 3, count: 3, stats: { count: 3, finished: 1 } },
        // hardcover source — shelved-but-not-owned books.
        { key: "hardcover", value: 5, count: 5, stats: { count: 5, finished: 2 } },
      ],
    };
  });
}

function stubConfig(booksEnabled: boolean) {
  server.use(
    http.get("/api/v1/config/public", () =>
      HttpResponse.json({
        registration_enabled: true,
        auth_provider: "local",
        oidc_enabled: false,
        billing_enabled: false,
        beta_flags: {},
        github_connect_enabled: false,
        books_enabled: booksEnabled,
      }),
    ),
  );
}

beforeEach(() => {
  runQueryMock.mockReset();
  wireQueries();
  // The page persists filter/group/sort in the URL (history.replaceState); jsdom's
  // location is shared across tests in a file, so reset it or a filter set by one
  // test leaks into the next test's initial state.
  window.history.replaceState(null, "", "/");
});
afterEach(() => vi.restoreAllMocks());

describe("BooksPage (merged groupable view)", () => {
  it("renders the flat leaf book table when no axis is grouped", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });

    // The DSL rows-mode leaves render as the flat table.
    expect(await screen.findByText("Project Hail Mary")).toBeInTheDocument();
    expect(screen.getByText("Dune")).toBeInTheDocument();

    // The hero derives its counts from the source-grouped query (8 + 3 + 5 = 16).
    expect(await screen.findByText("16")).toBeInTheDocument(); // Tracked total
    // No filter is active → cards show plain totals, not `<filtered>/<total>`.
    expect(screen.queryByText("/16")).toBeNull();

    // A flat leaf query ran (rows mode) — not api.getBooksItems.
    expect(
      runQueryMock.mock.calls.some((c) => (c[0] as QuerySpec).rows === true),
    ).toBe(true);
  });

  it("renders groups with count + rollups after adding the Author axis", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });
    await screen.findByText("Project Hail Mary");

    // Open the "Group by" picker and add the Author axis.
    await userEvent.click(screen.getByRole("button", { name: /Add axis/ }));
    await userEvent.click(screen.getByRole("button", { name: "Author" }));

    // The grouped query renders the author group with count + formatted runtime
    // (1320 min → "22h") + finished rollup.
    expect(await screen.findByText("Brandon Sanderson")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument(); // count badge
    expect(screen.getByText("22h")).toBeInTheDocument(); // runtime rollup

    // A grouped author query fired.
    expect(
      runQueryMock.mock.calls.some(
        (c) => (c[0] as QuerySpec).group === "author",
      ),
    ).toBe(true);
  });

  it("opens a book's Hardcover page from the leaf row action", async () => {
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });

    const cell = await screen.findByText("Project Hail Mary");
    // The trailing action for that row (an ASIN-precise Hardcover search).
    const rowEl = cell.closest("tr")!;
    await userEvent.click(
      within(rowEl).getByRole("button", { name: /Open .* on Hardcover/ }),
    );

    expect(openSpy).toHaveBeenCalledTimes(1);
    expect(openSpy.mock.calls[0][0]).toBe(
      "https://hardcover.app/search?q=B08GB58KD5",
    );
  });

  it("offers Hardcover in the Source filter and as a hero card", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });
    await screen.findByText("Project Hail Mary");

    // The Source dropdown lists Hardcover alongside All/Audible/Kindle.
    const source = screen.getByLabelText("Source");
    const opts = within(source)
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(opts).toEqual(["All", "Audible", "Kindle", "Hardcover"]);

    // The hero surfaces a HARDCOVER count card (source='hardcover' count = 5).
    expect(screen.getByText("5")).toBeInTheDocument();
    expect(screen.getAllByText("Hardcover").length).toBeGreaterThan(0);
  });

  it("shows filtered/total hero counts once a filter is active", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });
    await screen.findByText("Project Hail Mary");

    // Baseline: plain totals, no filtered half.
    expect(screen.getByText("16")).toBeInTheDocument();
    expect(screen.queryByText("/16")).toBeNull();

    // Activate the Source filter → a filter-scoped hero query fires and each
    // card renders `<filtered>/<total>` (the muted "/16" total half appears).
    await userEvent.selectOptions(screen.getByLabelText("Source"), "audible");
    expect(await screen.findByText("/16")).toBeInTheDocument();
  });

  it("keeps search, source, status, group-by and connect in one control bar", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });
    await screen.findByText("Project Hail Mary");

    // All controls live in the single consolidated bar.
    expect(
      screen.getByPlaceholderText(/Search title or author/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Source")).toBeInTheDocument();
    expect(screen.getByLabelText("Status")).toBeInTheDocument();
    expect(screen.getByText("Group by:")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Add axis/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Connect Amazon/ }),
    ).toBeInTheDocument();
  });

  it("shows the disabled state when the feature is off", async () => {
    stubConfig(false);
    renderWithProviders(<BooksPage />, { withRouter: true });

    expect(
      await screen.findByText("Books isn't enabled on this server"),
    ).toBeInTheDocument();
  });
});

describe("BooksPage tabs (Library | Reading Events)", () => {
  it("renders both tabs and defaults to the Library view", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });

    // Both tabs present.
    expect(await screen.findByRole("tab", { name: "Library" })).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Reading Events" }),
    ).toBeInTheDocument();

    // Library is the default active tab → the book table renders as today.
    expect(screen.getByRole("tab", { name: "Library" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(await screen.findByText("Project Hail Mary")).toBeInTheDocument();
    // The library-only "Match" filter is present; the events-only "Origin" is not.
    expect(screen.getByLabelText("Match")).toBeInTheDocument();
    expect(screen.queryByLabelText("Origin")).toBeNull();
  });

  it("switches to the Reading Events table and fires a readingEvents query", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });
    await screen.findByText("Project Hail Mary");

    await userEvent.click(screen.getByRole("tab", { name: "Reading Events" }));

    // The events tab is now active…
    expect(
      screen.getByRole("tab", { name: "Reading Events" }),
    ).toHaveAttribute("aria-selected", "true");
    // …and its distinct control (the Origin filter) is shown while the library's
    // Match filter is gone (the library subtree unmounted).
    expect(await screen.findByLabelText("Origin")).toBeInTheDocument();
    expect(screen.queryByLabelText("Match")).toBeNull();

    // A readingEvents query fired (the events table drives its own DSL domain).
    await waitFor(() =>
      expect(
        runQueryMock.mock.calls.some(
          (c) => (c[0] as QuerySpec).domain === "readingEvents",
        ),
      ).toBe(true),
    );

    // Switching back restores the Library view.
    await userEvent.click(screen.getByRole("tab", { name: "Library" }));
    expect(screen.getByRole("tab", { name: "Library" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(await screen.findByLabelText("Match")).toBeInTheDocument();
  });
});
