// MetricsTab.test.tsx — the generic rate-metrics dashboard (gaka-metrics).
// Non-tautological invariants:
//   1. Series from the mocked /admin/metrics fetch are GROUPED by name prefix
//      into Router / Rate-limiters / External APIs, and an unrecognized name
//      lands in "Other" (proving new metrics need zero FE wiring).
//   2. Each series renders a card titled by its short name with its current
//      (most-recent-bucket) rate.
//   3. An empty snapshot shows the "No metrics yet" empty state.
import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";

import { MetricsTab } from "@/features/admin/MetricsTab";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { MetricSeries } from "@/types/api";

function stubMetrics(series: MetricSeries[]) {
  server.use(
    http.get("/api/v1/admin/metrics", () => HttpResponse.json({ series })),
  );
}

const bucket = (v: number): MetricSeries["points"] => [
  { bucket: "2026-08-13T19:00:00Z", value: 0 },
  { bucket: "2026-08-13T19:01:00Z", value: v },
];

describe("MetricsTab", () => {
  it("groups series by subsystem and shows each series' current rate", async () => {
    stubMetrics([
      { name: "http.requests", kind: "counter", points: bucket(12) },
      {
        name: "jobs.limiter.acquired{kind=github-stats}",
        kind: "counter",
        points: bucket(3),
      },
      { name: "hardcover.calls", kind: "counter", points: bucket(5) },
      { name: "custom.widget.renders", kind: "counter", points: bucket(9) },
    ]);

    renderWithProviders(<MetricsTab />);

    // Group headers.
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "Router" })).toBeInTheDocument(),
    );
    expect(
      screen.getByRole("heading", { name: "Rate-limiters" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "External APIs" }),
    ).toBeInTheDocument();
    // The uncategorized series falls into "Other" — proves generic handling.
    expect(screen.getByRole("heading", { name: "Other" })).toBeInTheDocument();

    // Series card titles (short-named) + a current rate headline.
    expect(screen.getByTitle("http.requests")).toHaveTextContent("requests");
    expect(
      screen.getByTitle("jobs.limiter.acquired{kind=github-stats}"),
    ).toHaveTextContent("acquired{kind=github-stats}");
    // Current rate = last bucket value.
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("9")).toBeInTheDocument();
  });

  it("renders an empty state when there are no series", async () => {
    stubMetrics([]);
    renderWithProviders(<MetricsTab />);
    await waitFor(() =>
      expect(screen.getByText("No metrics yet")).toBeInTheDocument(),
    );
  });
});
