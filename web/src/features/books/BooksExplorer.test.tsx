// BooksExplorer tests — exercise the Explore-mode render pipeline with runQuery
// mocked, so we assert the real component behavior (ranking, Other roll-up,
// re-query on control changes, empty/error/loading) without a backend.
//
// Non-tautology anchors:
//   - Groups render value-desc AND the "Other" roll-up is pinned last and
//     UNRANKED (no rank number) even though its value outranks the named rows.
//   - Changing the dimension fires a NEW runQuery with the new `group`.
//   - Switching to the runtime measure strands "author" → the component must
//     fall back to a legal dim (source) and disable the author chip, so it never
//     issues the runtime×author combo the backend rejects.
//   - [] → empty state; a rejected query → error state; in-flight → skeleton.
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/renderWithProviders";
import { http, HttpResponse } from "@/test/msw/handlers";
import { server } from "@/test/msw/server";
import { BooksExplorer } from "@/features/books/BooksExplorer";
import type { QueryResult } from "@/lib/queryApi";

const { runQueryMock } = vi.hoisted(() => ({ runQueryMock: vi.fn() }));
vi.mock("@/lib/queryApi", () => ({
  // Only runQuery is consumed at runtime; the types are erased.
  runQuery: runQueryMock,
}));

const groups = (rows: Array<{ key: string; value: number }>): QueryResult => ({
  kind: "groups",
  groups: rows,
});

describe("BooksExplorer", () => {
  it("renders returned groups value-desc, with Other pinned last and unranked", async () => {
    runQueryMock.mockResolvedValue(
      groups([
        { key: "Brandon Sanderson", value: 12 },
        { key: "Terry Pratchett", value: 7 },
        // Other's value (20) beats the named rows, yet it stays last + unranked.
        { key: "Other", value: 20 },
      ]),
    );

    renderWithProviders(<BooksExplorer />);

    const table = await screen.findByTestId("explore-groups");
    expect(within(table).getByText("Brandon Sanderson")).toBeInTheDocument();
    expect(within(table).getByText("Terry Pratchett")).toBeInTheDocument();
    expect(within(table).getByText("Other")).toBeInTheDocument();

    // Values are formatted as counts (books measure).
    expect(within(table).getByText("12")).toBeInTheDocument();

    // DOM order = value-desc, Other last.
    const text = table.textContent ?? "";
    expect(text.indexOf("Brandon Sanderson")).toBeLessThan(
      text.indexOf("Terry Pratchett"),
    );
    expect(text.indexOf("Terry Pratchett")).toBeLessThan(text.indexOf("Other"));

    // Only two named rows are ranked (1, 2); Other is not rank "3".
    expect(within(table).getByText("1")).toBeInTheDocument();
    expect(within(table).getByText("2")).toBeInTheDocument();
    expect(within(table).queryByText("3")).toBeNull();
  });

  it("re-queries with the new group when the dimension changes", async () => {
    runQueryMock.mockResolvedValue(groups([{ key: "read", value: 5 }]));
    renderWithProviders(<BooksExplorer />);
    await screen.findByText("read");

    // Default dimension is author; the first call reflects that.
    expect(runQueryMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ group: "author", measure: "books" }),
    );

    runQueryMock.mockClear();
    runQueryMock.mockResolvedValue(groups([{ key: "audible", value: 9 }]));
    await userEvent.click(screen.getByRole("button", { name: /Source/ }));

    await screen.findByText("audible");
    expect(runQueryMock).toHaveBeenCalledWith(
      expect.objectContaining({ group: "source", measure: "books" }),
    );
  });

  it("falls back off author + disables it when switching to the runtime measure", async () => {
    runQueryMock.mockResolvedValue(groups([{ key: "audible", value: 3 }]));
    renderWithProviders(<BooksExplorer />);
    await screen.findByText("audible");

    runQueryMock.mockClear();
    runQueryMock.mockResolvedValue(groups([{ key: "audible", value: 180 }]));
    await userEvent.click(screen.getByRole("button", { name: "Runtime" }));

    // author is illegal for runtime → the component re-queries source×runtime.
    await waitFor(() =>
      expect(runQueryMock).toHaveBeenCalledWith(
        expect.objectContaining({ group: "source", measure: "runtime" }),
      ),
    );
    // ...and the author chip is disabled so it can't be re-selected.
    expect(screen.getByRole("button", { name: /Author/ })).toBeDisabled();
    // runtime value renders as h/m, not a raw count.
    expect(await screen.findByText("3h")).toBeInTheDocument();
  });

  it("shows the empty state when the query returns no groups", async () => {
    runQueryMock.mockResolvedValue(groups([]));
    renderWithProviders(<BooksExplorer />);
    expect(
      await screen.findByText(/Nothing to break down yet/),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("explore-groups")).toBeNull();
  });

  it("shows the error state when the query rejects", async () => {
    runQueryMock.mockRejectedValue(new Error("boom"));
    renderWithProviders(<BooksExplorer />);
    expect(
      await screen.findByText(/Couldn't run that breakdown/),
    ).toBeInTheDocument();
  });

  it("shows a loading skeleton while the query is in flight", () => {
    // Never resolves → stays in the loading branch.
    runQueryMock.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<BooksExplorer />);
    expect(screen.getByTestId("explore-skeleton")).toBeInTheDocument();
  });

  // gaka-canon: each NAMED group row carries a pin toggle (canonical entities);
  // the "Other" roll-up does not. Clicking it pins that value on the ACTIVE
  // grouping dimension (default author) via the curation create endpoint.
  it("renders a pin toggle per named row (not on Other) and pins with axis=dim", async () => {
    runQueryMock.mockResolvedValue(
      groups([
        { key: "Brandon Sanderson", value: 12 },
        { key: "Terry Pratchett", value: 7 },
        { key: "Other", value: 20 },
      ]),
    );
    let posted: unknown;
    server.use(
      http.post("/api/v1/users/current/curation", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ rule: { id: 1 } });
      }),
    );

    renderWithProviders(<BooksExplorer />);
    await screen.findByTestId("explore-groups");

    // Two named rows → two pin toggles; Other is not pinnable.
    const toggles = screen.getAllByTestId("pin-toggle");
    expect(toggles).toHaveLength(2);

    // Click the first (Brandon Sanderson) → pins on the default author dimension.
    await userEvent.click(toggles[0]);
    await waitFor(() => expect(posted).toBeDefined());
    expect(posted).toMatchObject({
      axis: "author",
      action: "pin",
      matchType: "exact",
      matchValue: "Brandon Sanderson",
    });
  });
});
