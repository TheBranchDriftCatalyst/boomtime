import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useLogsSocket } from "@/hooks/useLogsSocket";
import { mockLogsWs, type MockLogsWs } from "@/test/ws";
import type { ServerLogEntry } from "@/types/api";

let ws: MockLogsWs | undefined;

afterEach(() => {
  ws?.stop();
  ws = undefined;
});

function logEntry(over: Partial<ServerLogEntry> = {}): ServerLogEntry {
  return {
    id: 1,
    time: "2026-07-10T00:00:00Z",
    level: "INFO",
    msg: "hello",
    ...over,
  };
}

describe("useLogsSocket (mock-socket)", () => {
  it("disabled -> closed, no connection", () => {
    const { result } = renderHook(() => useLogsSocket(false));
    expect(result.current.status).toBe("closed");
    expect(result.current.logs).toHaveLength(0);
  });

  it("snapshot backfills logs and opens", async () => {
    ws = mockLogsWs();
    const { result } = renderHook(() => useLogsSocket());

    await ws.connected();
    await waitFor(() => expect(result.current.status).toBe("open"));

    act(() =>
      ws!.send({
        type: "snapshot",
        logs: [logEntry({ id: 1, msg: "a" }), logEntry({ id: 2, msg: "b" })],
      }),
    );

    await waitFor(() => expect(result.current.logs).toHaveLength(2));
    expect(result.current.logs[0].msg).toBe("a");
    expect(result.current.logs[1].msg).toBe("b");
  });

  it("appends live log entries after snapshot", async () => {
    ws = mockLogsWs();
    const { result } = renderHook(() => useLogsSocket());
    await ws.connected();

    act(() => ws!.send({ type: "snapshot", logs: [] }));
    act(() => ws!.send({ type: "log", log: logEntry({ id: 3, msg: "live" }) }));

    await waitFor(() => expect(result.current.logs).toHaveLength(1));
    expect(result.current.logs[0].msg).toBe("live");
  });

  it("de-duplicates entries by monotonic id (backfill overlap)", async () => {
    ws = mockLogsWs();
    const { result } = renderHook(() => useLogsSocket());
    await ws.connected();

    act(() =>
      ws!.send({
        type: "snapshot",
        logs: [logEntry({ id: 1 }), logEntry({ id: 2 })],
      }),
    );
    await waitFor(() => expect(result.current.logs).toHaveLength(2));

    // A reconnect backfill re-sends id 2 plus a new id 3; id 2 must not dupe.
    act(() =>
      ws!.send({
        type: "snapshot",
        logs: [logEntry({ id: 2 }), logEntry({ id: 3, msg: "new" })],
      }),
    );
    await waitFor(() => expect(result.current.logs).toHaveLength(3));
    expect(result.current.logs[2].msg).toBe("new");
  });

  it("ignores malformed (non-JSON) messages", async () => {
    ws = mockLogsWs();
    const { result } = renderHook(() => useLogsSocket());
    await ws.connected();
    act(() => ws!.send({ type: "snapshot", logs: [logEntry({ id: 1 })] }));
    await waitFor(() => expect(result.current.logs).toHaveLength(1));

    act(() => ws!.sendRaw("}{not json"));
    expect(result.current.logs).toHaveLength(1);
  });

  it("clear() empties the local buffer", async () => {
    ws = mockLogsWs();
    const { result } = renderHook(() => useLogsSocket());
    await ws.connected();
    act(() => ws!.send({ type: "snapshot", logs: [logEntry({ id: 1 })] }));
    await waitFor(() => expect(result.current.logs).toHaveLength(1));

    act(() => result.current.clear());
    expect(result.current.logs).toHaveLength(0);
  });

  it("caps logs at 2000 lines (keeps the tail)", async () => {
    ws = mockLogsWs();
    const { result } = renderHook(() => useLogsSocket());
    await ws.connected();
    const big = Array.from({ length: 2100 }, (_, i) =>
      logEntry({ id: i + 1, msg: `l${i}` }),
    );
    act(() => ws!.send({ type: "snapshot", logs: big }));

    await waitFor(() => expect(result.current.logs.length).toBe(2000));
    expect(result.current.logs[result.current.logs.length - 1].msg).toBe(
      "l2099",
    );
  });
});
