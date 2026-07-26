// useImageJobQueue.test.ts — verify the hook's WS lifecycle handling.
//
// The suite mocks the global WebSocket constructor to a controllable
// FakeSocket so tests can:
//   - deliver JSON messages on demand (snapshot / added / updated / removed)
//   - simulate onclose to observe the auto-reconnect scheduling
//
// What we do NOT test here:
//   - the enqueue() function's HTTP call — that's exercised by api.test.ts
//     against the real request() and doesn't need a hook to observe.
//   - render-loop side effects — the AdminTab consumer covers that via
//     Playwright / manual verification. Here we just assert the hook's
//     public state converges to the expected Map after each wire event.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useImageJobQueue } from "./useImageJobQueue";

// FakeSocket — minimal WebSocket surface: only the fields the hook reads
// or writes. Adding more real WebSocket surface as needed is trivial;
// keep it small so the test intent stays obvious.
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

  // Test helpers ----------------------------------------------------------
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
  // The hook constructs a WebSocket via `new WebSocket(url)`. Vitest's
  // stubGlobal makes the assignment survive the frozen-globals check that
  // some environments (happy-dom under strict mode) apply.
  vi.stubGlobal("WebSocket", FakeSocket as unknown as typeof WebSocket);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("useImageJobQueue", () => {
  it("applies the initial snapshot to jobs map", async () => {
    const { result } = renderHook(() => useImageJobQueue());
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const sock = FakeSocket.latest();

    act(() => {
      sock.fireOpen();
      sock.fireMessage({
        kind: "snapshot",
        jobs: [
          {
            id: "j1",
            labelId: "late-night-coder",
            status: "queued",
            enqueuedAt: "2026-07-26T12:00:00Z",
          },
          {
            id: "j2",
            labelId: "polyglot",
            status: "running",
            enqueuedAt: "2026-07-26T12:00:01Z",
          },
        ],
      });
    });

    expect(result.current.jobs.size).toBe(2);
    expect(result.current.jobs.get("j1")?.status).toBe("queued");
    expect(result.current.jobs.get("j2")?.status).toBe("running");
    expect(result.current.connected).toBe(true);
  });

  it("added/updated events mutate the map, removed drops the entry", async () => {
    const { result } = renderHook(() => useImageJobQueue());
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
          id: "j-new",
          labelId: "machine",
          status: "queued",
          enqueuedAt: "2026-07-26T12:00:02Z",
        },
      });
    });
    expect(result.current.jobs.get("j-new")?.status).toBe("queued");

    act(() => {
      sock.fireMessage({
        kind: "updated",
        job: {
          id: "j-new",
          labelId: "machine",
          status: "running",
          enqueuedAt: "2026-07-26T12:00:02Z",
          startedAt: "2026-07-26T12:00:03Z",
        },
      });
    });
    expect(result.current.jobs.get("j-new")?.status).toBe("running");
    expect(result.current.jobs.get("j-new")?.startedAt).toBe("2026-07-26T12:00:03Z");

    act(() => {
      sock.fireMessage({
        kind: "removed",
        job: { id: "j-new", labelId: "machine", status: "done", enqueuedAt: "" },
      });
    });
    expect(result.current.jobs.has("j-new")).toBe(false);
  });

  it("byLabel returns the most-recently-enqueued job for a label", async () => {
    const { result } = renderHook(() => useImageJobQueue());
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const sock = FakeSocket.latest();
    act(() => {
      sock.fireOpen();
      sock.fireMessage({
        kind: "snapshot",
        jobs: [
          {
            id: "old",
            labelId: "sprinter",
            status: "error",
            enqueuedAt: "2026-07-26T12:00:00Z",
            finishedAt: "2026-07-26T12:00:20Z",
          },
          {
            id: "new",
            labelId: "sprinter",
            status: "running",
            enqueuedAt: "2026-07-26T12:00:30Z",
          },
        ],
      });
    });
    const cur = result.current.byLabel("sprinter");
    expect(cur?.id).toBe("new");
    expect(cur?.status).toBe("running");
  });

  it("reconnects on close after backoff and resets the connected flag", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useImageJobQueue());
    // The initial WebSocket is constructed inside a useEffect and shows up
    // after React flushes; advance timers to let that happen. When using
    // fake timers, waitFor won't tick real time — call runOnlyPendingTimers
    // to let the effect flush its microtasks.
    await vi.waitFor(() => expect(FakeSocket.instances.length).toBe(1));

    const first = FakeSocket.latest();
    act(() => first.fireOpen());
    expect(result.current.connected).toBe(true);

    act(() => first.fireClose());
    expect(result.current.connected).toBe(false);
    // Backoff kicks in — first delay is 500ms.
    act(() => {
      vi.advanceTimersByTime(500);
    });
    await vi.waitFor(() => expect(FakeSocket.instances.length).toBe(2));
    expect(result.current.reconnectAttempt).toBeGreaterThan(0);
  });

  it("closes the socket on unmount", async () => {
    const { unmount } = renderHook(() => useImageJobQueue());
    await waitFor(() => expect(FakeSocket.instances.length).toBe(1));
    const sock = FakeSocket.latest();
    act(() => sock.fireOpen());
    unmount();
    expect(sock.closed).toBe(true);
  });
});
