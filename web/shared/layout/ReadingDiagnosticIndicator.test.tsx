// ReadingDiagnosticIndicator.test.tsx — the app-wide nav beacon lights ONLY
// while the user-scoped status endpoint reports calibrating:true. We mock the
// api layer and assert both the dark (nothing rendered) and lit states.
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@shared/lib/api";
import type { ReadingMonitorStatus } from "@shared/types/api";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { ReadingDiagnosticIndicator } from "./ReadingDiagnosticIndicator";

vi.mock("@shared/lib/api", () => ({
  api: { getReadingMonitorStatus: vi.fn() },
}));

const getStatus = vi.mocked(api.getReadingMonitorStatus);

function renderIndicator() {
  return renderWithProviders(
    <TooltipProvider>
      <ReadingDiagnosticIndicator />
    </TooltipProvider>,
    { withRouter: true },
  );
}

afterEach(() => vi.clearAllMocks());

function status(over: Partial<ReadingMonitorStatus> = {}): ReadingMonitorStatus {
  return { enabled: true, calibrating: false, calibratingUntil: null, ...over };
}

describe("ReadingDiagnosticIndicator", () => {
  it("renders nothing when not calibrating", async () => {
    getStatus.mockResolvedValue(status({ calibrating: false }));
    renderIndicator();

    // Give the query a tick to settle, then assert the beacon stayed dark.
    await waitFor(() => expect(getStatus).toHaveBeenCalled());
    expect(
      screen.queryByTestId("reading-diagnostic-indicator"),
    ).not.toBeInTheDocument();
  });

  it("lights the beacon while calibrating", async () => {
    getStatus.mockResolvedValue(
      status({
        calibrating: true,
        calibratingUntil: new Date(Date.now() + 10 * 60_000).toISOString(),
      }),
    );
    renderIndicator();

    const beacon = await screen.findByTestId("reading-diagnostic-indicator");
    expect(beacon).toHaveAttribute(
      "aria-label",
      expect.stringMatching(/calibration running/i),
    );
  });
});
