// useBackfillJobQueue.test.ts — mirror of useImageJobQueue.test.ts for
// the git-history backfill WS hook (gaka-vh8).
//
// FakeSocket is copy-pasted to keep the two files independent; a
// future refactor could share it, but the divergence risk (each hook's
// hook-specific message shapes might drift) isn't worth the coupling.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useBackfillJobQueue } from "./useBackfillJobQueue";

class FakeSocket {
  static instances: FakeSocket[] = [];
  static latest(): FakeSocket {
    const s = FakeSocket.instances.at(-1);
    if (!s) throw new Error("no FakeSocket instance created");
    return s;
  }
  static reset() {
    FakeSocket.instances = [];
  }

  onopen: ((ev?: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev?: Event) => void) | null = null;
  onclose: ((ev?: CloseEvent) => void) | null = null;
  readyState = 0;
  closed = false;
  url: string;

  constructor(url: string) {
    this.url = url;
    FakeSocket.instances.push(this);
  }

  fireOpen() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }
  fireMessage(data: unknown) {
    const evt = new MessageEvent("message", { data: JSON.stringify(data) });
    this.onmessage?.(evt);
  }
  fireClose() {
    this.readyState = 3;
    this.closed = true;
    this.onclose?.(new CloseEvent("close"));
  }
  close() {
    this.closed = true;
    this.readyState = 3;
  }
}

beforeEach(() => {
  FakeSocket.reset();
  vi.stubGlobal("WebSocket", FakeSocket as unknown as typeof WebSocket);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("useBackfillJobQueue", () => {
  it("applies the initial snapshot to jobs map", async () => {
    const { result } = renderHook(() => useBackfillJobQueue());
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const sock = FakeSocket.latest();

    act(() => {
      sock.fireOpen();
      sock.fireMessage({
        kind: "snapshot",
        jobs: [
          {
            id: "j1",
            owner: "panda",
            repoName: "boomtime",
            repoPath: "/tmp/boomtime",
            status: "queued",
            total: 42,
            processed: 0,
            written: 0,
            skipped: 0,
            enqueuedAt: "2026-07-27T12:00:00Z",
          },
        ],
      });
    });

    await waitFor(() => expect(result.current.jobs.size).toBe(1));
    const j1 = result.current.jobs.get("j1");
    expect(j1?.repoName).toBe("boomtime");
    expect(j1?.status).toBe("queued");
    expect(result.current.connected).toBe(true);
  });

  it("applies added and updated events, and removes on removed", async () => {
    const { result } = renderHook(() => useBackfillJobQueue());
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const sock = FakeSocket.latest();

    act(() => {
      sock.fireOpen();
      sock.fireMessage({ kind: "snapshot", jobs: [] });
    });
    act(() => {
      sock.fireMessage({
        kind: "added",
        job: {
          id: "j1",
          owner: "panda",
          repoName: "boomtime",
          repoPath: "/tmp/boomtime",
          status: "queued",
          total: 42,
          processed: 0,
          written: 0,
          skipped: 0,
          enqueuedAt: "2026-07-27T12:00:00Z",
        },
      });
    });
    await waitFor(() => expect(result.current.jobs.size).toBe(1));

    act(() => {
      sock.fireMessage({
        kind: "updated",
        job: {
          id: "j1",
          owner: "panda",
          repoName: "boomtime",
          repoPath: "/tmp/boomtime",
          status: "running",
          total: 42,
          processed: 5,
          written: 100,
          skipped: 0,
          enqueuedAt: "2026-07-27T12:00:00Z",
          startedAt: "2026-07-27T12:00:05Z",
        },
      });
    });
    await waitFor(() => expect(result.current.jobs.get("j1")?.status).toBe("running"));
    expect(result.current.jobs.get("j1")?.written).toBe(100);

    act(() => {
      sock.fireMessage({
        kind: "removed",
        job: { id: "j1" },
      });
    });
    await waitFor(() => expect(result.current.jobs.size).toBe(0));
  });

  it("schedules a reconnect after onclose", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useBackfillJobQueue());
    await vi.waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const sock = FakeSocket.latest();

    act(() => {
      sock.fireOpen();
      sock.fireClose();
    });
    expect(result.current.connected).toBe(false);

    // First backoff = 500ms.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(FakeSocket.instances.length).toBeGreaterThanOrEqual(2);
  });
});
