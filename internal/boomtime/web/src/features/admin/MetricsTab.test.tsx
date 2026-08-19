// MetricsTab.test.tsx — the Prometheus gathered-view dashboard (gaka-metrics).
// Non-tautological invariants:
//   1. Families from the mocked /admin/metrics fetch are GROUPED by name prefix
//      into Router / Outbound / Rate-limiters / External APIs / Runtime, and an
//      unrecognized name lands in "Other" (proving new metrics need zero FE
//      wiring).
//   2. A counter sample renders its label set + cumulative value; a histogram
//      sample renders its count + derived average latency in ms (sum/count).
//   3. An empty snapshot shows the "No metrics yet" empty state.
import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";

import { MetricsTab } from "@boomtime/features/admin/MetricsTab";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import type { MetricFamily } from "@shared/types/api";

function stubMetrics(families: MetricFamily[]) {
  server.use(
    http.get("/api/v1/admin/metrics", () => HttpResponse.json({ families })),
  );
}

describe("MetricsTab", () => {
  it("groups families by subsystem and renders counter + histogram samples", async () => {
    stubMetrics([
      {
        name: "http_requests_total",
        type: "counter",
        samples: [
          {
            labels: { method: "GET", route: "/p/:slug", status_class: "2xx" },
            value: 12,
          },
        ],
      },
      {
        name: "http_request_duration_seconds",
        type: "histogram",
        // avg = 0.5s / 2 = 0.25s → 250 ms
        samples: [
          { labels: { method: "GET", route: "/p/:slug" }, count: 2, sum: 0.5 },
        ],
      },
      {
        name: "http_client_requests_total",
        type: "counter",
        samples: [
          { labels: { host: "api.github.com", method: "GET", status_class: "2xx" }, value: 4 },
        ],
      },
      {
        name: "jobs_limiter_events_total",
        type: "counter",
        samples: [{ labels: { kind: "github-stats", outcome: "acquired" }, value: 3 }],
      },
      {
        name: "hardcover_calls_total",
        type: "counter",
        samples: [{ labels: { outcome: "executed" }, value: 5 }],
      },
      {
        name: "go_goroutines",
        type: "gauge",
        samples: [{ value: 42 }],
      },
      {
        name: "custom_widget_renders_total",
        type: "counter",
        samples: [{ value: 9 }],
      },
    ]);

    renderWithProviders(<MetricsTab />);

    // Group headers.
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Router (incoming)" }),
      ).toBeInTheDocument(),
    );
    expect(
      screen.getByRole("heading", { name: "Outbound HTTP" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Rate-limiters" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "External APIs" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Runtime" })).toBeInTheDocument();
    // The uncategorized family falls into "Other" — proves generic handling.
    expect(screen.getByRole("heading", { name: "Other" })).toBeInTheDocument();

    // Family card titles (the raw metric name).
    expect(screen.getByText("http_requests_total")).toBeInTheDocument();
    // Counter sample value.
    expect(screen.getByText("12")).toBeInTheDocument();
    // Histogram sample → count + derived avg latency (0.5s/2 = 250 ms).
    expect(screen.getByText(/2 · avg 250 ms/)).toBeInTheDocument();
  });

  it("renders an empty state when there are no families", async () => {
    stubMetrics([]);
    renderWithProviders(<MetricsTab />);
    await waitFor(() =>
      expect(screen.getByText("No metrics yet")).toBeInTheDocument(),
    );
  });
});
