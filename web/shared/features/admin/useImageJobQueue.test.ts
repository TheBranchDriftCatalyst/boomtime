// useImageJobQueue.test.ts — the hook polls the DB-jobs status endpoint
// (gaka-hney Stage 3; it previously held a WebSocket). We mock
// api.getLabelImageStatus / api.regenerateLabelImages and assert the public
// state (jobs map keyed by labelId, byLabel, connected/reconnectAttempt,
// enqueue) converges correctly.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useImageJobQueue } from "./useImageJobQueue";
import { api } from "@shared/lib/api";

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("useImageJobQueue (DB-jobs polling)", () => {
  it("polls status → map keyed by labelId; connected flips true", async () => {
    vi.spyOn(api, "getLabelImageStatus").mockResolvedValue({
      jobs: [
        { labelId: "late-night-coder", status: "queued" },
        { labelId: "polyglot", status: "running", startedAt: "2026-07-26T12:00:03Z" },
      ],
    });
    const { result } = renderHook(() => useImageJobQueue());

    await waitFor(() => expect(result.current.connected).toBe(true));
    expect(result.current.jobs.size).toBe(2);
    expect(result.current.byLabel("late-night-coder")?.status).toBe("queued");
    expect(result.current.byLabel("polyglot")?.status).toBe("running");
    expect(result.current.byLabel("polyglot")?.startedAt).toBe("2026-07-26T12:00:03Z");
  });

  it("byLabel returns the label's current job, undefined for unknown", async () => {
    vi.spyOn(api, "getLabelImageStatus").mockResolvedValue({
      jobs: [{ labelId: "sprinter", status: "done", finishedAt: "2026-07-26T12:00:20Z" }],
    });
    const { result } = renderHook(() => useImageJobQueue());

    await waitFor(() => expect(result.current.jobs.size).toBe(1));
    expect(result.current.byLabel("sprinter")?.status).toBe("done");
    expect(result.current.byLabel("nope")).toBeUndefined();
  });

  it("a failed poll flips connected=false and bumps reconnectAttempt", async () => {
    vi.spyOn(api, "getLabelImageStatus").mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useImageJobQueue());

    await waitFor(() => expect(result.current.reconnectAttempt).toBeGreaterThan(0));
    expect(result.current.connected).toBe(false);
  });

  it("enqueue posts a regen and triggers an immediate re-poll", async () => {
    const statusSpy = vi
      .spyOn(api, "getLabelImageStatus")
      .mockResolvedValue({ jobs: [] });
    const regenSpy = vi.spyOn(api, "regenerateLabelImages").mockResolvedValue({
      queued: 1,
      jobs: [{ jobId: "7", labelId: "machine", existing: false }],
    });
    const { result } = renderHook(() => useImageJobQueue());

    await waitFor(() => expect(result.current.connected).toBe(true));
    const callsBefore = statusSpy.mock.calls.length;

    let out: { jobId: string; existing: boolean } | undefined;
    await act(async () => {
      out = await result.current.enqueue({ labelId: "machine", prompt: "p" });
    });

    expect(regenSpy).toHaveBeenCalledWith({
      entries: [{ id: "machine", prompt: "p" }],
      ids: ["machine"],
    });
    expect(out).toEqual({ jobId: "7", existing: false });
    // The immediate re-poll fired at least once more than the mount poll.
    expect(statusSpy.mock.calls.length).toBeGreaterThan(callsBefore);
  });
});
