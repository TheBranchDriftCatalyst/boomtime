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
  it("opens a book's Hardcover page in a new tab when its table row is clicked", async () => {
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    stub([
      item({
        title: "Project Hail Mary",
        authors: "Andy Weir",
        status: "reading",
        progressPercent: 30,
      }),
    ]);

    renderWithProviders(<BooksPage />, { withRouter: true });

    const cell = await screen.findByText("Project Hail Mary");
    await userEvent.click(cell);

    expect(openSpy).toHaveBeenCalledTimes(1);
    const [url, target, features] = openSpy.mock.calls[0];
    expect(url).toBe(
      "https://hardcover.app/search?q=" +
        encodeURIComponent("Project Hail Mary Andy Weir"),
    );
    expect(target).toBe("_blank");
    expect(features).toContain("noopener");
  });
});
