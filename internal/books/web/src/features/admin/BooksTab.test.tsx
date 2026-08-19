// BooksTab.test.tsx — rm2. Focused on the Raw feed view: it renders the RAW
// heartbeat/position stream from BOTH reading sources (Kindle position samples
// + Audible listening roll-ups). Starting on ?view=raw exercises the deep-link
// the nav indicator uses.
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@shared/lib/api";
import type { ReadingMonitorRaw } from "@shared/types/api";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { BooksTab } from "./BooksTab";

vi.mock("@shared/lib/api", () => ({
  api: { getReadingMonitorRaw: vi.fn() },
}));

const getRaw = vi.mocked(api.getReadingMonitorRaw);

const RAW: ReadingMonitorRaw = {
  kindle: [
    {
      asin: "B00KINDLE1",
      title: "The Kindle Book",
      location: 1420,
      dloc: 60,
      creationTime: new Date(Date.now() - 90_000).toISOString(),
      intervalSecs: 12,
    },
  ],
  audible: [
    { title: "The Audible Book", day: "2026-08-14", listeningSeconds: 3600 },
  ],
};

afterEach(() => vi.clearAllMocks());

describe("BooksTab · Raw feed", () => {
  it("renders both source streams from the raw endpoint", async () => {
    getRaw.mockResolvedValue(RAW);
    renderWithProviders(<BooksTab />, {
      withRouter: true,
      initialEntries: ["/app/admin/books?view=raw"],
    });

    // Kindle stream row + Audible stream row both render.
    expect(await screen.findByText("The Kindle Book")).toBeInTheDocument();
    expect(screen.getByText("B00KINDLE1")).toBeInTheDocument();
    expect(screen.getByText("+60")).toBeInTheDocument(); // Δloc
    expect(screen.getByText("The Audible Book")).toBeInTheDocument();
    expect(screen.getByText("3600")).toBeInTheDocument();

    // Both section headers are present.
    expect(screen.getByText(/Kindle · position stream/)).toBeInTheDocument();
    expect(screen.getByText(/Audible · listening stream/)).toBeInTheDocument();
  });

  it("shows an empty state when a source has no samples", async () => {
    getRaw.mockResolvedValue({ kindle: [], audible: [] });
    renderWithProviders(<BooksTab />, {
      withRouter: true,
      initialEntries: ["/app/admin/books?view=raw"],
    });

    const empties = await screen.findAllByText(/No recent .* samples yet/);
    expect(empties).toHaveLength(2);
  });
});
