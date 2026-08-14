// ReadingMonitorPanel.test.tsx — renders the live monitor tab with the stream
// hook MOCKED, so we assert (a) streamed samples render as delta rows, (b) the
// cadence stats derive from them, and (c) Start/Stop toggles the stream's
// `enabled` flag. The socket itself is covered server-side + by the pure
// cadence tests; here the component wiring is under test.
import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  ReadingMonitorStream,
  ReadingMonitorOptions,
} from "./useReadingMonitorSocket";
import type { RawSample } from "./readingMonitorCadence";

// Mutable state the mocked hook returns + the last options it was called with.
let currentStream: ReadingMonitorStream;
let lastOptions: ReadingMonitorOptions | undefined;

vi.mock("./useReadingMonitorSocket", () => ({
  useReadingMonitorSocket: (opts: ReadingMonitorOptions) => {
    lastOptions = opts;
    return currentStream;
  },
}));

// Imported AFTER the mock is registered.
import { ReadingMonitorPanel } from "./ReadingMonitorPanel";

const clear = vi.fn();

function streamWith(samples: RawSample[], status: ReadingMonitorStream["status"]): ReadingMonitorStream {
  return {
    samples,
    lastHeartbeat: samples.length
      ? { books: 1, polled: 1, sampledAt: samples[samples.length - 1].sampledAt }
      : null,
    info: "monitor live",
    errors: [],
    status,
    clear,
  };
}

const advancingSamples: RawSample[] = [
  {
    asin: "A",
    title: "Book A",
    location: 100,
    creationTime: "2026-08-13T10:00:00Z",
    sampledAt: "2026-08-13T10:00:00Z",
  },
  {
    asin: "A",
    title: "Book A",
    location: 150,
    creationTime: "2026-08-13T10:00:12Z",
    sampledAt: "2026-08-13T10:00:12Z",
  },
];

afterEach(() => {
  vi.clearAllMocks();
  lastOptions = undefined;
});

describe("ReadingMonitorPanel", () => {
  it("idle before Start: shows the idle empty state and the stream is disabled", () => {
    currentStream = streamWith([], "closed");
    render(<ReadingMonitorPanel />);
    expect(screen.getByText("Monitor idle")).toBeInTheDocument();
    expect(lastOptions?.enabled).toBe(false);
  });

  it("renders streamed samples as newest-first delta rows", () => {
    currentStream = streamWith(advancingSamples, "open");
    render(<ReadingMonitorPanel />);

    const table = screen.getByRole("table");
    const bodyRows = within(table).getAllByRole("row").slice(1); // drop header
    expect(bodyRows).toHaveLength(2);

    // Newest-first: the advance (location 150, Δloc +50) is the top row.
    expect(within(bodyRows[0]).getByText("150")).toBeInTheDocument();
    expect(within(bodyRows[0]).getByText("+50")).toBeInTheDocument();
    expect(within(bodyRows[0]).getByText("12s")).toBeInTheDocument(); // Δt

    // The baseline (first) sample has no delta.
    expect(within(bodyRows[1]).getByText("100")).toBeInTheDocument();
  });

  it("derives the cadence stats from the samples", () => {
    currentStream = streamWith(advancingSamples, "open");
    render(<ReadingMonitorPanel />);

    // Advances tile shows 1 (one advance between the two samples). getByText
    // returns the label row; its parent is the tile holding the value.
    const advances = screen.getByText("Advances").parentElement as HTMLElement;
    expect(within(advances).getByText("1")).toBeInTheDocument();

    // Median interval = 12s.
    const median = screen.getByText("Median interval").parentElement as HTMLElement;
    expect(within(median).getByText("12s")).toBeInTheDocument();

    // Avg Δloc = +50.
    const avg = screen.getByText("Avg Δloc").parentElement as HTMLElement;
    expect(within(avg).getByText("+50")).toBeInTheDocument();
  });

  it("Start/Stop toggles the stream enabled flag", () => {
    currentStream = streamWith([], "closed");
    const { rerender } = render(<ReadingMonitorPanel />);

    // Start.
    expect(lastOptions?.enabled).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /start/i }));
    expect(lastOptions?.enabled).toBe(true);
    // Start clears any prior buffer for a fresh measurement.
    expect(clear).toHaveBeenCalled();

    // Now running: the Stop button is shown.
    currentStream = streamWith([], "open");
    rerender(<ReadingMonitorPanel />);
    const stop = screen.getByRole("button", { name: /stop/i });
    fireEvent.click(stop);
    expect(lastOptions?.enabled).toBe(false);
  });
});
