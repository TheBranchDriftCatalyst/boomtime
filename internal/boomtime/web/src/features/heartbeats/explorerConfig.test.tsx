import { describe, expect, it } from "vitest";
import { pathToFilters, toGroupPage } from "@boomtime/features/heartbeats/explorerConfig";
import type { HeartbeatGroupPayload } from "@shared/types/api";

describe("heartbeats explorer source adapter", () => {
  it("maps a drill path to heartbeat filters, dropping null steps", () => {
    expect(
      pathToFilters([
        { dim: "project", value: "catalyst" },
        { dim: "day", value: "2026-01-01" },
      ]),
    ).toEqual({ project: "catalyst", day: "2026-01-01" });

    // A null-valued step is dropped (the backend's absent = "no filter"
    // convention), never sent as an ambiguous filter.
    expect(
      pathToFilters([
        { dim: "project", value: "catalyst" },
        { dim: "language", value: null },
      ]),
    ).toEqual({ project: "catalyst" });
  });

  it("maps a group payload to {value, stats:{count, seconds}}", () => {
    const payload: HeartbeatGroupPayload = {
      groupBy: "project",
      groups: [
        {
          value: "catalyst",
          count: 3,
          seconds: 120,
          firstSeen: "2026-01-01T00:00:00Z",
          lastSeen: "2026-01-02T00:00:00Z",
        },
        {
          value: null,
          count: 1,
          seconds: 5,
          firstSeen: "2026-01-01T00:00:00Z",
          lastSeen: "2026-01-01T00:00:05Z",
        },
      ],
      truncated: true,
    };

    expect(toGroupPage(payload)).toEqual({
      groups: [
        { value: "catalyst", stats: { count: 3, seconds: 120 } },
        { value: null, stats: { count: 1, seconds: 5 } },
      ],
      truncated: true,
    });
  });

  it("defaults truncated to false when absent", () => {
    const payload: HeartbeatGroupPayload = {
      groupBy: "day",
      groups: [{ value: "2026-01-01", count: 2, seconds: 60, firstSeen: "", lastSeen: "" }],
    };
    expect(toGroupPage(payload).truncated).toBe(false);
  });
});
