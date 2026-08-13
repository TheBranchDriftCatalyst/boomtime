// ReadingTiles.test.tsx — each Reading dashboard tile is one (or two) query-DSL
// specs; these tests mock `runQuery` and assert the tile maps the returned
// QueryResult into the right DOM (scalar → formatted KPI, groups → donut/bar
// labels+values, series → trend points/month bars), plus the empty + error
// ladders. Non-tautological: the assertions read the MAPPED, formatted output,
// not the raw fixture.
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/renderWithProviders";
import type { QueryResult, QuerySpec } from "@/lib/queryApi";

// Mock the DSL client so no network/auth runs; react-query still drives the
// loading→data ladder through the real hook.
vi.mock("@/lib/queryApi", () => ({ runQuery: vi.fn() }));
import { runQuery } from "@/lib/queryApi";
import {
  BooksByGenreTile,
  FinishedPerMonthTile,
  ListeningThisWeekTile,
  ListeningTrendTile,
  ProlificGenresTile,
  TopSeriesByRuntimeTile,
} from "./ReadingTiles";

const mockRun = vi.mocked(runQuery);

afterEach(() => {
  mockRun.mockReset();
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
    expect(screen.getByText("Listening this week")).toBeInTheDocument();
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
