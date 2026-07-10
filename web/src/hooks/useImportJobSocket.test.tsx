import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useImportJobSocket } from "@/hooks/useImportJobSocket";
import { mockImportWs, type MockImportWs } from "@/test/ws";
import { importJob, importLog } from "@/test/factories";
import type { ImportJob } from "@/types/api";

let ws: MockImportWs | undefined;

afterEach(() => {
  ws?.stop();
  ws = undefined;
});

describe("useImportJobSocket (mock-socket) — P0", () => {
  it("null jobId -> closed, no connection", () => {
    const { result } = renderHook(() => useImportJobSocket(null));
    expect(result.current.status).toBe("closed");
    expect(result.current.job).toBeNull();
  });

  it("snapshot replaces job + logs and opens", async () => {
    ws = mockImportWs(7);
    const { result } = renderHook(() => useImportJobSocket(7));

    await ws.connected();
    await waitFor(() => expect(result.current.status).toBe("open"));

    act(() =>
      ws!.send({
        type: "snapshot",
        job: importJob({ id: 7, processedDays: 3 }) as ImportJob,
        logs: [importLog({ id: 1, message: "hi" })],
      }),
    );

    await waitFor(() => expect(result.current.job?.id).toBe(7));
    expect(result.current.job?.processedDays).toBe(3);
    expect(result.current.logs).toHaveLength(1);
    expect(result.current.logs[0].message).toBe("hi");
  });

  it("log appends; progress + state merge counters", async () => {
    ws = mockImportWs(1);
    const { result } = renderHook(() => useImportJobSocket(1));
    await ws.connected();

    act(() =>
      ws!.send({
        type: "snapshot",
        job: importJob({ id: 1, processedDays: 0 }) as ImportJob,
        logs: [],
      }),
    );
    await waitFor(() => expect(result.current.job?.id).toBe(1));

    act(() => ws!.send({ type: "log", log: importLog({ id: 2, message: "a" }) }));
    act(() => ws!.send({ type: "log", log: importLog({ id: 3, message: "b" }) }));
    await waitFor(() => expect(result.current.logs).toHaveLength(2));

    act(() =>
      ws!.send({
        type: "progress",
        job: importJob({ id: 1, processedDays: 12, importedCount: 999 }) as ImportJob,
      }),
    );
    await waitFor(() => expect(result.current.job?.processedDays).toBe(12));
    expect(result.current.job?.importedCount).toBe(999);
  });

  it("ignores malformed (non-JSON) messages", async () => {
    ws = mockImportWs(2);
    const { result } = renderHook(() => useImportJobSocket(2));
    await ws.connected();
    act(() =>
      ws!.send({ type: "snapshot", job: importJob({ id: 2 }) as ImportJob, logs: [] }),
    );
    await waitFor(() => expect(result.current.job?.id).toBe(2));

    act(() => ws!.sendRaw("}{not json"));
    // No crash, state unchanged.
    expect(result.current.job?.id).toBe(2);
  });

  it("terminal state stops reconnection (status stays closed after close)", async () => {
    ws = mockImportWs(3);
    const { result } = renderHook(() => useImportJobSocket(3));
    await ws.connected();

    act(() =>
      ws!.send({
        type: "state",
        job: importJob({ id: 3, state: "completed" }) as ImportJob,
      }),
    );

    await waitFor(() => expect(result.current.job?.state).toBe("completed"));
    // The hook closes the socket on terminal; status settles to closed and
    // does NOT flip to "reconnecting".
    await waitFor(() => expect(result.current.status).toBe("closed"));
    expect(result.current.status).not.toBe("reconnecting");
  });

  it("caps logs at 2000 lines", async () => {
    ws = mockImportWs(4);
    const { result } = renderHook(() => useImportJobSocket(4));
    await ws.connected();
    const bigLogs = Array.from({ length: 2100 }, (_, i) =>
      importLog({ id: i, message: `l${i}` }),
    );
    act(() =>
      ws!.send({
        type: "snapshot",
        job: importJob({ id: 4 }) as ImportJob,
        logs: bigLogs,
      }),
    );
    await waitFor(() => expect(result.current.logs.length).toBe(2000));
    // Kept the most recent (tail).
    expect(result.current.logs[result.current.logs.length - 1].message).toBe(
      "l2099",
    );
  });
});
