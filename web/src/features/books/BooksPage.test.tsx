// BooksPage.test.tsx — focused coverage for the row → Hardcover interaction
// (gaka-qic0). Renders the real page with the feature enabled and a stubbed
// library, then asserts a table-row click opens that book's Hardcover page in a
// new tab. The config + items are stubbed via MSW so the whole page pipeline
// (config gate → fetch → filter/sort → table) runs for real.
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { BooksPage } from "./BooksPage";
import type { ReadingItemDTO } from "@/types/meta";

function item(partial: Partial<ReadingItemDTO>): ReadingItemDTO {
  return {
    source: "audible",
    externalId: Math.random().toString(36).slice(2),
    title: "Untitled",
    authors: "",
    status: "reading",
    progressPercent: 0,
    finished: false,
    syncedAt: "2026-08-01T00:00:00Z",
    ...partial,
  };
}

/** Turn the feature on + stub the library in one call. */
function stub(items: ReadingItemDTO[]) {
  server.use(
    http.get("/api/v1/config/public", () =>
      HttpResponse.json({
        registration_enabled: true,
        auth_provider: "local",
        oidc_enabled: false,
        billing_enabled: false,
        beta_flags: {},
        github_connect_enabled: false,
        books_enabled: true,
      }),
    ),
    http.get("/api/v1/books/items", () => HttpResponse.json({ items })),
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("BooksPage", () => {
  it("opens an ASIN-precise Hardcover search when an unmatched row is clicked", async () => {
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    stub([
      item({
        title: "Project Hail Mary",
        authors: "Andy Weir",
        externalId: "B08GB58KD5", // the ASIN — more exact than a title search
        status: "reading",
        progressPercent: 30,
      }),
    ]);

    renderWithProviders(<BooksPage />, { withRouter: true });

    const cell = await screen.findByText("Project Hail Mary");
    await userEvent.click(cell);

    expect(openSpy).toHaveBeenCalledTimes(1);
    const [url, target, features] = openSpy.mock.calls[0];
    expect(url).toBe("https://hardcover.app/search?q=B08GB58KD5");
    expect(target).toBe("_blank");
    expect(features).toContain("noopener");
  });

  it("links direct to the Hardcover book page for a matched row", async () => {
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    stub([
      item({
        title: "Dune",
        authors: "Frank Herbert",
        externalId: "B0ASIN123",
        hardcoverBookId: 987654,
        hardcoverStatus: "read",
        status: "read",
        finished: true,
      }),
    ]);

    renderWithProviders(<BooksPage />, { withRouter: true });

    const cell = await screen.findByText("Dune");
    await userEvent.click(cell);

    expect(openSpy).toHaveBeenCalledTimes(1);
    expect(openSpy.mock.calls[0][0]).toBe("https://hardcover.app/books/987654");
  });

  it("renders the Hardcover match-state column: 'Not matched' vs a match badge", async () => {
    stub([
      item({ title: "Unmatched Book", externalId: "B0UNMATCH", authors: "Nobody" }),
      item({
        title: "Matched Book",
        externalId: "B0MATCHED",
        authors: "Somebody",
        hardcoverBookId: 42,
        hardcoverStatus: "read",
      }),
    ]);

    renderWithProviders(<BooksPage />, { withRouter: true });

    // Column header exists.
    expect(await screen.findByText("Hardcover")).toBeInTheDocument();
    // Unmatched row shows the honest muted state.
    expect(screen.getByText("Not matched")).toBeInTheDocument();
    // Matched row surfaces its shelf status ("read" → "Read").
    expect(screen.getByText("Read")).toBeInTheDocument();
  });
});
