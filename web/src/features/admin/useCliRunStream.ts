import { useCallback, useEffect, useRef, useState } from "react";
import type { TerminalStatus } from "@/components/TerminalLogViewer";

export interface CliRunBody {
  command: string;
  flags: Record<string, unknown>;
  confirm?: string;
}

export interface CliStreamState {
  status: TerminalStatus;
  output: string;
  dryRun: boolean;
  exitError: string;
  durationMs: number;
  truncated: boolean;
}

const INITIAL: CliStreamState = {
  status: "idle",
  output: "",
  dryRun: false,
  exitError: "",
  durationMs: 0,
  truncated: false,
};

// One frame of the /cli/run/ws stream (mirrors internal/admin/cli_run_ws.go).
interface CliStreamMsg {
  type: "start" | "output" | "done" | "error";
  dryRun?: boolean;
  data?: string;
  exitError?: string;
  durationMs?: number;
  truncated?: boolean;
  error?: string;
}

/** useCliRunStream (gaka-hney.5): opens the /api/v1/admin/cli/run/ws socket,
 * sends one run request, and accumulates the live output + terminal status so a
 * <TerminalLogViewer> can render it. Cookie-authed (same-origin WS). */
export function useCliRunStream() {
  const [state, setState] = useState<CliStreamState>(INITIAL);
  const wsRef = useRef<WebSocket | null>(null);

  const start = useCallback((body: CliRunBody) => {
    wsRef.current?.close();
    setState({ ...INITIAL, status: "running" });

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(
      `${proto}//${window.location.host}/api/v1/admin/cli/run/ws`,
    );
    wsRef.current = ws;

    ws.onopen = () => ws.send(JSON.stringify(body));
    ws.onmessage = (e) => {
      let msg: CliStreamMsg;
      try {
        msg = JSON.parse(e.data as string);
      } catch {
        return;
      }
      setState((s) => {
        switch (msg.type) {
          case "start":
            return { ...s, dryRun: Boolean(msg.dryRun) };
          case "output":
            return { ...s, output: s.output + (msg.data ?? "") };
          case "done":
            return {
              ...s,
              status: msg.exitError ? "error" : "done",
              exitError: msg.exitError ?? "",
              durationMs: msg.durationMs ?? 0,
              truncated: Boolean(msg.truncated),
            };
          case "error":
            return { ...s, status: "error", exitError: msg.error ?? "run refused" };
          default:
            return s;
        }
      });
    };
    // A close while still "running" means we never got a terminal frame.
    ws.onclose = () =>
      setState((s) =>
        s.status === "running"
          ? {
              ...s,
              status: "error",
              exitError: s.exitError || "connection closed before the run finished",
            }
          : s,
      );
  }, []);

  const reset = useCallback(() => {
    wsRef.current?.close();
    setState(INITIAL);
  }, []);

  useEffect(() => () => wsRef.current?.close(), []);

  return { ...state, start, reset, active: state.status !== "idle" };
}
