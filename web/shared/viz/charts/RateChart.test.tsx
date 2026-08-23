// RateChart.test.tsx — the generic rate-over-time chart (boom-metrics).
// Non-tautological invariants:
//   1. A series WITH points mounts a chart surface (an <svg>) without throwing
//      — jsdom has no layout so the D3 draw is width-gated, but the surface
//      wrapper + svg node are present.
//   2. A series with NO points renders the shared EmptyChart placeholder, not a
//      broken/blank surface.
//   3. currentRate reports the most-recent bucket value (the "now" rate), and 0
//      for an empty series.
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RateChart, currentRate } from "./RateChart";
import type { MetricSeries } from "@shared/types/api";

const withPoints: MetricSeries = {
  name: "http.requests",
  kind: "counter",
  points: [
    { bucket: "2026-08-13T19:00:00Z", value: 4 },
    { bucket: "2026-08-13T19:01:00Z", value: 0 },
    { bucket: "2026-08-13T19:02:00Z", value: 7 },
  ],
};

describe("RateChart", () => {
  it("renders a chart surface (svg) for a series with points", () => {
    const { container } = render(<RateChart series={withPoints} />);
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("renders the EmptyChart placeholder for a series with no points", () => {
    const empty: MetricSeries = { name: "amazon.calls", kind: "counter", points: [] };
    const { container, getByText } = render(<RateChart series={empty} />);
    expect(container.querySelector("svg")).toBeNull();
    expect(getByText("No data available")).toBeInTheDocument();
  });
});

describe("currentRate", () => {
  it("returns the most-recent bucket value", () => {
    expect(currentRate(withPoints)).toBe(7);
  });
  it("returns 0 for an empty series", () => {
    expect(currentRate({ name: "x", kind: "counter", points: [] })).toBe(0);
  });
});
