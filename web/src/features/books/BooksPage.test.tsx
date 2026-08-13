// BooksPage.test.tsx — the merged groupable Books view (gaka-02sh Track C).
// runQuery is mocked (discriminated on the spec) so the real page pipeline runs:
//   - groupBy [] → the flat leaf table (DSL rows mode) renders book rows;
//   - adding an axis (Author) → the DSL grouped query renders group rows with
//     the count + runtime + finished rollups;
//   - a leaf row's trailing action opens that book's Hardcover page.
// The public config (books_enabled) is stubbed via MSW like the rest of the app.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { QuerySpec } from "@/lib/queryApi";
import { BooksPage } from "./BooksPage";

const { runQueryMock } = vi.hoisted(() => ({ runQueryMock: vi.fn() }));
vi.mock("@/lib/queryApi", () => ({ runQuery: runQueryMock }));

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
// ranked author groups (with rollups); any other group (source) → the hero /
// source breakdown.
function wireQueries() {
  runQueryMock.mockImplementation(async (spec: QuerySpec) => {
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
    // source grouping (hero + source axis).
    return {
      kind: "groups",
      groups: [
        { key: "audible", value: 8, count: 8, stats: { count: 8, finished: 4 } },
        { key: "kindle", value: 3, count: 3, stats: { count: 3, finished: 1 } },
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
});
afterEach(() => vi.restoreAllMocks());

describe("BooksPage (merged groupable view)", () => {
  it("renders the flat leaf book table when no axis is grouped", async () => {
    stubConfig(true);
    renderWithProviders(<BooksPage />, { withRouter: true });

    // The DSL rows-mode leaves render as the flat table.
    expect(await screen.findByText("Project Hail Mary")).toBeInTheDocument();
    expect(screen.getByText("Dune")).toBeInTheDocument();

    // The hero derives its counts from the source-grouped query (8 + 3 = 11).
    expect(await screen.findByText("11")).toBeInTheDocument(); // Tracked

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

  it("shows the disabled state when the feature is off", async () => {
    stubConfig(false);
    renderWithProviders(<BooksPage />, { withRouter: true });

    expect(
      await screen.findByText("Books isn't enabled on this server"),
    ).toBeInTheDocument();
  });
});
