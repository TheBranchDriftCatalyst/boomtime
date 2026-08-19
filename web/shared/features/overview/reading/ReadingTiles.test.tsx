// ReadingTiles.test.tsx — each Reading dashboard tile is one (or two) query-DSL
// specs; these tests mock `runQuery` and assert the tile maps the returned
// QueryResult into the right DOM (scalar → formatted KPI, groups → donut/bar
// labels+values, series → trend points/month bars), plus the empty + error
// ladders. Non-tautological: the assertions read the MAPPED, formatted output,
// not the raw fixture.
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { server } from "@shared/test/msw/server";
import { colorAt } from "@shared/viz/d3/color";
import type { QueryResult, QuerySpec } from "@shared/lib/queryApi";

// Mock the DSL client so no network/auth runs; react-query still drives the
// loading→data ladder through the real hook.
vi.mock("@shared/lib/queryApi", () => ({ runQuery: vi.fn() }));
import { runQuery } from "@shared/lib/queryApi";
import {
  BooksByGenreTile,
  FinishedPerMonthTile,
  ListeningThisWeekTile,
  ListeningTrendTile,
  ProlificGenresTile,
  TopSeriesByRuntimeTile,
} from "./ReadingTiles";
import { ReadingRangeControl } from "./ReadingRangeControl";
import { __resetReadingRange } from "./readingRange";

const mockRun = vi.mocked(runQuery);

afterEach(() => {
  mockRun.mockReset();
  // The range store is a module singleton — reset it so one test's selection
  // never leaks into the next.
  __resetReadingRange();
});

/** Resolve every runQuery call to one fixed result. */
function resolveWith(result: QueryResult) {
  mockRun.mockResolvedValue(result);
}

describe("ListeningThisWeekTile", () => {
  it("formats the scalar seconds as hours/min", async () => {
    resolveWith({ kind: "scalar", scalar: 3 * 3600 + 12 * 60 }); // 3h 12m
    renderWithProviders(<ListeningThisWeekTile />);
    expect(await screen.findByText("3h 12m")).toBeInTheDocument();
    expect(screen.getByText("Listening in range")).toBeInTheDocument();
  });

  it("queries the default window (12w → 84 days) on first render", async () => {
    resolveWith({ kind: "scalar", scalar: 0 });
    renderWithProviders(<ListeningThisWeekTile />);
    await waitFor(() =>
      expect(mockRun).toHaveBeenCalledWith(
        expect.objectContaining({
          domain: "reading",
          measure: "seconds",
          over: { granularity: "none", range: { lastN: 84 } },
        }),
      ),
    );
  });
});

describe("BooksByGenreTile", () => {
  it("renders a legend of the returned genre groups + counts", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "Science Fiction", value: 6 },
        { key: "Fantasy", value: 4 },
      ],
    });
    renderWithProviders(<BooksByGenreTile />);
    expect(await screen.findByText("Science Fiction")).toBeInTheDocument();
    expect(screen.getByText("Fantasy")).toBeInTheDocument();
    const legend = screen.getByTestId("reading-donut-legend");
    expect(legend).toHaveTextContent("6");
    expect(legend).toHaveTextContent("4");
  });

  it("shows the empty state for []", async () => {
    resolveWith({ kind: "groups", groups: [] });
    renderWithProviders(<BooksByGenreTile />);
    expect(await screen.findByText("No data available")).toBeInTheDocument();
    expect(screen.queryByTestId("reading-donut")).not.toBeInTheDocument();
  });

  it("shows an error message when the query rejects", async () => {
    mockRun.mockRejectedValue(new Error("boom"));
    renderWithProviders(<BooksByGenreTile />);
    expect(await screen.findByText("Failed to load genres.")).toBeInTheDocument();
  });

  // gaka-canon: the genre donut legend exposes a pin toggle per genre (never on
  // the "Other" roll-up). Clicking pins that genre via the curation endpoint.
  it("renders a pin toggle per genre legend row (not Other) and pins with axis=genre", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "Science Fiction", value: 6 },
        { key: "Fantasy", value: 4 },
        { key: "Other", value: 9 },
      ],
    });
    let posted: unknown;
    server.use(
      http.post("/api/v1/users/current/curation", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ rule: { id: 1 } });
      }),
    );

    renderWithProviders(<BooksByGenreTile />);
    await screen.findByText("Science Fiction");

    // Two named genres → two toggles; the Other slice has none.
    const legend = screen.getByTestId("reading-donut-legend");
    const toggles = within(legend).getAllByTestId("pin-toggle");
    expect(toggles).toHaveLength(2);

    await userEvent.click(toggles[0]);
    await waitFor(() => expect(posted).toBeDefined());
    expect(posted).toMatchObject({
      axis: "genre",
      action: "pin",
      matchType: "exact",
      matchValue: "Science Fiction",
    });
  });
});

describe("TopSeriesByRuntimeTile", () => {
  it("renders runtime bars with formatted minutes", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "The Expanse", value: 600 }, // 600 min → 10h
        { key: "Dune", value: 90 }, // 1h 30m
      ],
    });
    renderWithProviders(<TopSeriesByRuntimeTile />);
    expect(await screen.findByText("The Expanse")).toBeInTheDocument();
    expect(screen.getByText("10h")).toBeInTheDocument();
    expect(screen.getByText("1h 30m")).toBeInTheDocument();
    expect(screen.getAllByTestId("reading-bar")).toHaveLength(2);
  });

  it("colors each bar from the shared colorAt palette (not the plain default)", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "The Expanse", value: 600 },
        { key: "Dune", value: 90 },
      ],
    });
    renderWithProviders(<TopSeriesByRuntimeTile />);
    await screen.findByText("The Expanse");
    const fills = screen.getAllByTestId("reading-bar-fill");
    // Each bar carries a SOLID color (from the shared palette) — not the primary
    // gradient the un-colored default falls back to...
    fills.forEach((fill) => {
      const bg = fill.style.background;
      expect(bg).not.toContain("linear-gradient");
      expect(bg).toMatch(/^(rgb|#)/);
    });
    // ...and the palette is positional, so adjacent bars differ.
    expect(fills[0].style.background).not.toBe(fills[1].style.background);
    expect(colorAt(0)).not.toBe(colorAt(1));
  });
});

describe("ProlificGenresTile", () => {
  it("renders each genre with a pluralized book count", async () => {
    resolveWith({
      kind: "groups",
      groups: [
        { key: "Sci-Fi", value: 5 },
        { key: "History", value: 1 },
      ],
    });
    renderWithProviders(<ProlificGenresTile />);
    expect(await screen.findByText("Sci-Fi")).toBeInTheDocument();
    expect(screen.getByText("5 books")).toBeInTheDocument();
    expect(screen.getByText("1 book")).toBeInTheDocument();
  });
});

describe("FinishedPerMonthTile", () => {
  it("renders a bar per month bucket with the count", async () => {
    resolveWith({
      kind: "series",
      series: [
        { bucket: "2026-01-01T00:00:00Z", value: 3 },
        { bucket: "2026-02-01T00:00:00Z", value: 2 },
      ],
    });
    renderWithProviders(<FinishedPerMonthTile />);
    expect(await screen.findByText("Jan 2026")).toBeInTheDocument();
    expect(screen.getByText("Feb 2026")).toBeInTheDocument();
    expect(screen.getAllByTestId("reading-bar")).toHaveLength(2);
    // Bars are neon-colored per position, not the plain gradient default.
    const fills = screen.getAllByTestId("reading-bar-fill");
    fills.forEach((fill) => {
      expect(fill.style.background).not.toContain("linear-gradient");
      expect(fill.style.background).toMatch(/^(rgb|#)/);
    });
    expect(fills[0].style.background).not.toBe(fills[1].style.background);
  });
});

describe("Reading date-range control", () => {
  it("re-scopes the windowed query when a different window is picked", async () => {
    resolveWith({ kind: "scalar", scalar: 0 });
    // Control + a windowed tile share the module store; picking a window must
    // change the runQuery range the tile issues.
    renderWithProviders(
      <>
        <ReadingRangeControl />
        <ListeningThisWeekTile />
      </>,
    );

    // Default 12W → 84-day window.
    await waitFor(() =>
      expect(mockRun).toHaveBeenCalledWith(
        expect.objectContaining({
          over: { granularity: "none", range: { lastN: 84 } },
        }),
      ),
    );

    mockRun.mockClear();
    resolveWith({ kind: "scalar", scalar: 0 });
    const control = screen.getByTestId("reading-range-control");
    await userEvent.click(within(control).getByRole("radio", { name: "6M" }));

    // 6M → 182-day window; the tile re-issues runQuery with the new range.
    await waitFor(() =>
      expect(mockRun).toHaveBeenCalledWith(
        expect.objectContaining({
          over: { granularity: "none", range: { lastN: 182 } },
        }),
      ),
    );
    // The picked segment is reflected as checked.
    expect(within(control).getByRole("radio", { name: "6M" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });
});

describe("ListeningTrendTile", () => {
  it("renders both the listening series and the coding overlay", async () => {
    // Two specs (reading + coding); return a distinct series per domain.
    mockRun.mockImplementation((spec: QuerySpec) =>
      Promise.resolve({
        kind: "series",
        series:
          spec.domain === "coding"
            ? [
                { bucket: "2026-05-01T00:00:00Z", value: 100 },
                { bucket: "2026-05-08T00:00:00Z", value: 200 },
              ]
            : [
                { bucket: "2026-05-01T00:00:00Z", value: 3600 },
                { bucket: "2026-05-08T00:00:00Z", value: 7200 },
                { bucket: "2026-05-15T00:00:00Z", value: 1800 },
              ],
      } satisfies QueryResult),
    );
    renderWithProviders(<ListeningTrendTile />);
    // 3 reading points + 2 coding points once both resolve.
    await waitFor(() =>
      expect(screen.getAllByTestId("trend-point")).toHaveLength(5),
    );
    const legend = screen.getByTestId("reading-trend-legend");
    expect(legend).toHaveTextContent("Listening");
    expect(legend).toHaveTextContent("Coding");
  });

  it("shows the empty state when there is no listening activity", async () => {
    resolveWith({ kind: "series", series: [] });
    renderWithProviders(<ListeningTrendTile />);
    expect(await screen.findByText("No data available")).toBeInTheDocument();
  });
});
