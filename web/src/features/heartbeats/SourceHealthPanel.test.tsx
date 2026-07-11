import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { SourceHealthPanel } from "@/features/heartbeats/SourceHealthPanel";
import {
  deriveSourceStatus,
  relativeTime,
  STATUS_RANK,
} from "@/lib/sourceStatus";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { SourceHealth } from "@/types/api";

const HOUR = 3600_000;
const DAY = 24 * HOUR;

describe("deriveSourceStatus thresholds", () => {
  const now = new Date("2026-07-10T12:00:00Z");
  const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();

  it("active ≤ 24h", () => {
    expect(deriveSourceStatus(ago(2 * HOUR), now)).toBe("active");
    expect(deriveSourceStatus(ago(23 * HOUR), now)).toBe("active");
  });
  it("idle > 24h and ≤ 7d", () => {
    expect(deriveSourceStatus(ago(2 * DAY), now)).toBe("idle");
    expect(deriveSourceStatus(ago(7 * DAY), now)).toBe("idle");
  });
  it("stale > 7d and ≤ 30d", () => {
    expect(deriveSourceStatus(ago(10 * DAY), now)).toBe("stale");
    expect(deriveSourceStatus(ago(30 * DAY), now)).toBe("stale");
  });
  it("silent > 30d", () => {
    expect(deriveSourceStatus(ago(31 * DAY), now)).toBe("silent");
    expect(deriveSourceStatus(ago(400 * DAY), now)).toBe("silent");
  });
});

describe("relativeTime", () => {
  const now = new Date("2026-07-10T12:00:00Z");
  const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();
  it("formats minutes/hours/days", () => {
    expect(relativeTime(ago(30_000), now)).toBe("just now");
    expect(relativeTime(ago(5 * 60_000), now)).toBe("5m ago");
    expect(relativeTime(ago(2 * HOUR), now)).toBe("2h ago");
    expect(relativeTime(ago(3 * DAY), now)).toBe("3d ago");
  });
});

function mockSources(sources: SourceHealth[]) {
  server.use(
    http.get("/api/v1/users/current/sources/health", () =>
      HttpResponse.json({ sources }),
    ),
  );
}

describe("SourceHealthPanel (rendering)", () => {
  it("renders a status pill per (plugin,machine) and sorts silent/stale first", async () => {
    const iso = (ms: number) => new Date(Date.now() - ms).toISOString();
    mockSources([
      { plugin: "vim-wakatime", machine: "laptop", lastSeen: iso(2 * HOUR), count: 10 },
      { plugin: "vscode-wakatime", machine: "desktop", lastSeen: iso(40 * DAY), count: 5 },
      { plugin: "chrome-wakatime", machine: "laptop", lastSeen: iso(10 * DAY), count: 3 },
    ]);

    renderWithProviders(<SourceHealthPanel />);

    await waitFor(() =>
      expect(
        screen.getByTestId("status-vscode-wakatime@desktop"),
      ).toBeInTheDocument(),
    );

    // Status derivation surfaced in the pills.
    expect(screen.getByTestId("status-vscode-wakatime@desktop")).toHaveTextContent(/silent/i);
    expect(screen.getByTestId("status-chrome-wakatime@laptop")).toHaveTextContent(/stale/i);
    expect(screen.getByTestId("status-vim-wakatime@laptop")).toHaveTextContent(/active/i);

    // Silent/stale sort first: vscode@desktop (silent) → chrome@laptop (stale) → vim@laptop (active).
    const pills = screen.getAllByTestId(/^status-/);
    const order = pills.map((p) => p.getAttribute("data-testid"));
    expect(order).toEqual([
      "status-vscode-wakatime@desktop",
      "status-chrome-wakatime@laptop",
      "status-vim-wakatime@laptop",
    ]);
  });

  it("shows an empty hint when there are no sources", async () => {
    mockSources([]);
    renderWithProviders(<SourceHealthPanel />);
    await waitFor(() =>
      expect(screen.getByText(/No ingestion sources yet/i)).toBeInTheDocument(),
    );
  });

  it("STATUS_RANK orders silent/stale before idle/active", () => {
    expect(STATUS_RANK.silent).toBeLessThan(STATUS_RANK.stale);
    expect(STATUS_RANK.stale).toBeLessThan(STATUS_RANK.idle);
    expect(STATUS_RANK.idle).toBeLessThan(STATUS_RANK.active);
  });

  it("renders the plugin @ machine label", async () => {
    mockSources([
      {
        plugin: "vim-wakatime",
        machine: "laptop",
        lastSeen: new Date().toISOString(),
        count: 1,
      },
    ]);
    renderWithProviders(<SourceHealthPanel />);
    const row = await screen.findByTestId("status-vim-wakatime@laptop");
    const label = row.parentElement as HTMLElement;
    expect(within(label).getByText("vim-wakatime")).toBeInTheDocument();
    expect(within(label).getByText(/@ laptop/)).toBeInTheDocument();
  });
});
