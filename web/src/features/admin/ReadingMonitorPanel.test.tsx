// ReadingMonitorPanel.test.tsx — the panel is now a THIN control over the
// SERVER-side reading monitor. We mock the api layer and assert: (a) live status
// renders from GET, (b) the on/off toggle PUTs {enabled}, (c) the mode switch
// PUTs {mode}, and (d) the Grafana cadence deep-link is present. The poll engine
// itself lives server-side, so there's nothing socket-shaped to test here.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";
import type { ReadingMonitorState } from "@/types/api";
import { ReadingMonitorPanel } from "./ReadingMonitorPanel";

vi.mock("@/lib/api", () => ({
  api: {
    getReadingMonitor: vi.fn(),
    setReadingMonitor: vi.fn(),
  },
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

const getReadingMonitor = vi.mocked(api.getReadingMonitor);
const setReadingMonitor = vi.mocked(api.setReadingMonitor);

function state(over: Partial<ReadingMonitorState> = {}): ReadingMonitorState {
  return {
    enabled: false,
    mode: "debounced",
    activeBooks: 0,
    lastPingAt: null,
    recommendation: null,
    ...over,
  };
}

function renderPanel() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <ReadingMonitorPanel />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.clearAllMocks();
});

describe("ReadingMonitorPanel", () => {
  it("renders live status from GET", async () => {
    getReadingMonitor.mockResolvedValue(
      state({ enabled: true, activeBooks: 3, lastPingAt: new Date().toISOString() }),
    );
    renderPanel();

    // Header status pill flips to "running" and the active-books tile shows 3.
    expect(await screen.findByText("running")).toBeInTheDocument();
    const activeBooks = screen.getByText("Active books").parentElement as HTMLElement;
    expect(activeBooks).toHaveTextContent("3");
    // The ON label reflects the fetched enabled state.
    expect(screen.getByText(/Reading monitor: ON/)).toBeInTheDocument();
  });

  it("the on/off toggle PUTs {enabled}", async () => {
    getReadingMonitor.mockResolvedValue(state({ enabled: false }));
    setReadingMonitor.mockResolvedValue(state({ enabled: true }));
    renderPanel();

    // Controls are disabled during the initial load; wait for the fetched
    // state to settle (switch enabled) before toggling.
    const sw = await screen.findByTestId("reading-monitor-switch");
    await waitFor(() => expect(sw).not.toBeDisabled());
    fireEvent.click(sw);

    await waitFor(() =>
      expect(setReadingMonitor).toHaveBeenCalledWith({ enabled: true }),
    );
  });

  it("the mode switch PUTs {mode}", async () => {
    getReadingMonitor.mockResolvedValue(state({ mode: "debounced" }));
    setReadingMonitor.mockResolvedValue(state({ mode: "verbose" }));
    renderPanel();

    const verbose = await screen.findByRole("button", { name: /verbose/i });
    await waitFor(() => expect(verbose).not.toBeDisabled());
    fireEvent.click(verbose);

    await waitFor(() =>
      expect(setReadingMonitor).toHaveBeenCalledWith({ mode: "verbose" }),
    );
  });

  it("renders the optimal-polling recommendation from GET", async () => {
    getReadingMonitor.mockResolvedValue(
      state({
        recommendation: {
          detectSecs: 30,
          captureSecs: 6,
          idleSecs: 300,
          medianAdvanceSecs: 12,
          p90AdvanceSecs: 45,
          sampleCount: 8,
        },
      }),
    );
    renderPanel();

    const rec = await screen.findByTestId("reading-monitor-recommendation");
    // The plain-English answer states each interval verbatim on the page.
    expect(rec).toHaveTextContent("8");
    expect(rec).toHaveTextContent("~30s");
    expect(rec).toHaveTextContent("~6s");
    expect(rec).toHaveTextContent("~300s");
    expect(rec).toHaveTextContent("~12s");
    expect(rec).toHaveTextContent("~45s");
  });

  it("shows the calibrating fallback when recommendation is null", async () => {
    getReadingMonitor.mockResolvedValue(state({ recommendation: null }));
    renderPanel();

    const empty = await screen.findByTestId(
      "reading-monitor-recommendation-empty",
    );
    // The empty block first shows "Loading calibration…"; once the null-rec
    // response settles it states the calibrate-me fallback.
    await waitFor(() => expect(empty).toHaveTextContent(/not enough data yet/i));
  });

  it("shows the Grafana cadence deep-link to the reading-monitor board", async () => {
    getReadingMonitor.mockResolvedValue(state());
    renderPanel();

    const link = await screen.findByRole("link", { name: /cadence dashboard/i });
    expect(link.getAttribute("href")).toContain("/d/boomtime-reading-monitor");
  });
});
