import { useEffect, useRef, useState } from "react";
import type {
  ImportJob,
  ImportLogLine,
  ImportSocketMessage,
} from "@/types/api";
import { isTerminalState } from "@/types/api";

// Keep the terminal from growing unbounded.
const MAX_LOG_LINES = 2000;

export type SocketStatus = "connecting" | "open" | "reconnecting" | "closed";

export interface ImportJobStream {
  job: ImportJob | null;
  logs: ImportLogLine[];
  status: SocketStatus;
}

function wsUrl(jobId: number): string {
  // Build from window.location so it works behind the vite proxy and in prod.
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/import/jobs/${jobId}/ws`;
}

function appendLog(prev: ImportLogLine[], line: ImportLogLine): ImportLogLine[] {
  const next = [...prev, line];
  return next.length > MAX_LOG_LINES
    ? next.slice(next.length - MAX_LOG_LINES)
    : next;
}

/**
 * Subscribes to a durable import job's live stream over WebSocket.
 *
 * The HttpOnly refresh_token cookie authenticates the handshake automatically
 * (same-origin), so no token is placed in the URL. On (re)connect the server
 * sends a fresh `snapshot`, which fully replaces local state — making reloads
 * and dropped connections resume seamlessly. Auto-reconnects with exponential
 * backoff until the job reaches a terminal state.
 */
export function useImportJobSocket(jobId: number | null): ImportJobStream {
  const [job, setJob] = useState<ImportJob | null>(null);
  const [logs, setLogs] = useState<ImportLogLine[]>([]);
  const [status, setStatus] = useState<SocketStatus>("closed");

  // Refs let the reconnect closure read current values without re-subscribing.
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<number | null>(null);
  const attemptRef = useRef(0);
  const terminalRef = useRef(false);

  useEffect(() => {
    if (jobId == null) {
      setJob(null);
      setLogs([]);
      setStatus("closed");
      return;
    }

    let cancelled = false;
    terminalRef.current = false;
    attemptRef.current = 0;

    const clearReconnect = () => {
      if (reconnectTimer.current != null) {
        window.clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
    };

    const scheduleReconnect = () => {
      if (cancelled || terminalRef.current) return;
      const attempt = attemptRef.current++;
      // 0.5s, 1s, 2s, 4s ... capped at 15s.
      const delay = Math.min(500 * 2 ** attempt, 15_000);
      setStatus("reconnecting");
      reconnectTimer.current = window.setTimeout(connect, delay);
    };

    const connect = () => {
      if (cancelled || terminalRef.current) return;
      setStatus((s) => (s === "reconnecting" ? s : "connecting"));

      const ws = new WebSocket(wsUrl(jobId));
      socketRef.current = ws;

      ws.onopen = () => {
        if (cancelled) return;
        attemptRef.current = 0;
        setStatus("open");
        // The server pushes a fresh snapshot on connect; nothing to send.
      };

      ws.onmessage = (event) => {
        if (cancelled) return;
        let msg: ImportSocketMessage;
        try {
          msg = JSON.parse(event.data as string) as ImportSocketMessage;
        } catch {
          return;
        }

        switch (msg.type) {
          case "snapshot":
            // Full re-sync: replace both job and logs.
            setJob(msg.job);
            setLogs(
              msg.logs.length > MAX_LOG_LINES
                ? msg.logs.slice(msg.logs.length - MAX_LOG_LINES)
                : msg.logs,
            );
            if (isTerminalState(msg.job.state)) terminalRef.current = true;
            break;
          case "log":
            setLogs((prev) => appendLog(prev, msg.log));
            break;
          case "progress":
            // Merge updated counters; keep the latest job snapshot.
            setJob((prev) => ({ ...(prev ?? msg.job), ...msg.job }));
            break;
          case "state":
            setJob((prev) => ({ ...(prev ?? msg.job), ...msg.job }));
            if (isTerminalState(msg.job.state)) {
              terminalRef.current = true;
              clearReconnect();
              ws.close();
            }
            break;
        }
      };

      ws.onclose = () => {
        if (cancelled) return;
        socketRef.current = null;
        if (terminalRef.current) {
          setStatus("closed");
        } else {
          scheduleReconnect();
        }
      };

      ws.onerror = () => {
        // onclose fires next and drives the reconnect.
        ws.close();
      };
    };

    connect();

    return () => {
      cancelled = true;
      clearReconnect();
      if (socketRef.current) {
        socketRef.current.onclose = null;
        socketRef.current.onerror = null;
        socketRef.current.close();
        socketRef.current = null;
      }
      setStatus("closed");
    };
  }, [jobId]);

  return { job, logs, status };
}
