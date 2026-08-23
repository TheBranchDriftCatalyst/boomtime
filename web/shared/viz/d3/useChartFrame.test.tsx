// useChartFrame.test.tsx (boom-3nw) — regression: the ResizeObserver must bind
// when the measured host div mounts LATER than the first render.
//
// The real bug: charts render <EmptyChart> (which mounts no ChartSurface, so
// useChartFrame's ref never attaches) while their query loads, then swap in
// <ChartSurface> once data arrives. The old `useRef` + `[]`-effect observed
// exactly once at mount, found a null ref, and bailed forever → `frame.width`
// pinned at 0 → useD3Surface's `frame.width === 0` guard skipped the D3 draw →
// permanently blank chart. Coding-punchcard was the reliable victim: its query
// is the slowest (~2.2s), so it always lost the load race.
//
// jsdom has no layout, so `frame.width` here comes from useChartFrame's
// on-attach `setWidth(node.clientWidth)` (the test setup's ResizeObserver is a
// no-op that never fires). We stub clientWidth to a deterministic value and
// assert width is measured EVEN when the host mounts on a later render — the
// exact behavior the old code got wrong.
import { render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useChartFrame } from "./useChartFrame";

const STUB_WIDTH = 640;

beforeEach(() => {
  // Shadow Element.prototype.clientWidth (0 in jsdom) with a positive value so
  // the on-attach measurement is deterministic.
  Object.defineProperty(HTMLDivElement.prototype, "clientWidth", {
    configurable: true,
    get: () => STUB_WIDTH,
  });
});
afterEach(() => {
  delete (HTMLDivElement.prototype as { clientWidth?: unknown }).clientWidth;
});

// Mirrors a real chart: a no-ChartSurface branch (attach=false) during loading,
// then the ref'd host (attach=true) once data arrives.
function Probe({ attach }: { attach: boolean }) {
  const { ref, frame } = useChartFrame(200);
  return (
    <div>
      <span data-testid="width">{frame.width}</span>
      {attach ? <div data-testid="host" ref={ref} /> : <span>loading…</span>}
    </div>
  );
}

describe("useChartFrame (boom-3nw)", () => {
  it("measures width when the host mounts LATER than the first render", async () => {
    const { getByTestId, rerender } = render(<Probe attach={false} />);
    // EmptyChart phase — no host mounted, nothing measured.
    expect(getByTestId("width").textContent).toBe("0");

    // Data arrives; the host (ChartSurface) mounts now. The callback ref must
    // bind the observer and measure. The old useRef + []-effect left this at 0
    // forever, which is what blanked the punchcard.
    rerender(<Probe attach={true} />);
    await waitFor(() =>
      expect(getByTestId("width").textContent).toBe(String(STUB_WIDTH)),
    );
  });

  it("measures width when the host is present from the first render", async () => {
    const { getByTestId } = render(<Probe attach />);
    await waitFor(() =>
      expect(getByTestId("width").textContent).toBe(String(STUB_WIDTH)),
    );
  });
});
