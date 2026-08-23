// LinesOfCodeCard.test.tsx (boom-yfg) — the LOC widget's headline/empty/loading
// states. The self-fetch hook is mocked so the test exercises the presentation
// (compact formatting, per-project rows, gentle empty state) without a network
// or an OverviewDataProvider.
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { LocPayload } from "@shared/types/stats";

const mockLoc = vi.fn();
vi.mock("@shared/features/overview/overviewWidgets", () => ({
  useOverviewLoc: () => mockLoc(),
}));

import { LinesOfCodeCard } from "./LinesOfCodeCard";

function setLoc(data: LocPayload | undefined, isLoading = false) {
  mockLoc.mockReturnValue({ data, isLoading });
}

describe("LinesOfCodeCard", () => {
  it("shows a gentle empty state (not an error) when there is no line data", () => {
    setLoc({ totalLoc: 0, perProject: [], overTime: [] });
    render(<LinesOfCodeCard />);
    expect(screen.getByText(/no line-count data in this range yet/i)).toBeInTheDocument();
  });

  it("shows a loading note while fetching", () => {
    setLoc(undefined, true);
    render(<LinesOfCodeCard />);
    expect(screen.getByText(/measuring lines of code/i)).toBeInTheDocument();
  });

  it("renders a compact total and the per-project breakdown", () => {
    setLoc({
      totalLoc: 1_240_000,
      perProject: [
        { project: "alpha", loc: 1_000_000 },
        { project: "beta", loc: 240_000 },
      ],
      overTime: [
        { date: "2025-06-01", loc: 900_000 },
        { date: "2025-06-08", loc: 1_240_000 },
      ],
    });
    render(<LinesOfCodeCard />);
    // Compact headline (Intl compact → "1.2M").
    expect(screen.getByText("1.2M")).toBeInTheDocument();
    // Per-project rows present.
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("beta")).toBeInTheDocument();
    // Project-count caption.
    expect(screen.getByText(/across 2 projects/i)).toBeInTheDocument();
  });
});
