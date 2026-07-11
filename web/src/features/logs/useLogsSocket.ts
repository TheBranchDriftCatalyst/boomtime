import { useEffect, useRef, useState } from "react";
import { authStore } from "@/lib/auth";
import type { ServerLogEntry, ServerLogSocketMessage } from "@/types/api";

// Keep the terminal from growing unbounded.
const MAX_LOG_LINES = 2000;

export type SocketStatus = "connecting" | "open" | "reconnecting" | "closed";

export interface LogsStream {
  logs: ServerLogEntry[];
  status: SocketStatus;
  /** Clear the local buffer (does not affect the server ring buffer). */
  clear: () => void;
}

// Build the WS URL from window.location so it works behind the vite proxy and
// in prod. The access token goes in a query param because browsers can't set an
// Authorization header on a WS handshake. afterId resumes after the last-seen
// entry so a reconnect only backfills what we missed.
function wsUrl(afterId: number): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams();
  const token = authStore.getSnapshot().token;
  if (token) params.set("token", token);
  if (afterId > 0) params.set("afterId", String(afterId));
  const qs = params.toString();
  return `${proto}//${window.location.host}/api/v1/logs/ws${qs ? `?${qs}` : ""}`;
}

// Append a batch of entries, de-duplicating by monotonic id and capping length.
function mergeLogs(
  prev: ServerLogEntry[],
  incoming: ServerLogEntry[],
): ServerLogEntry[] {
  if (incoming.length === 0) return prev;
  const seen = new Set(prev.map((l) => l.id));
  const next = [...prev];
  for (const line of incoming) {
    if (!seen.has(line.id)) {
      seen.add(line.id);
      next.push(line);
    }
  }
  return next.length > MAX_LOG_LINES
    ? next.slice(next.length - MAX_LOG_LINES)
    : next;
}

/**
 * Subscribes to the server process's own log stream over WebSocket.
 *
 * Mirrors useImportJobSocket: on (re)connect the server sends a `snapshot`
 * (ring-buffer backfill after the last-seen id) followed by live `log` entries.
 * Entries are de-duplicated by their monotonic `id`, so reconnecting with an
 * `afterId` resumes seamlessly across reloads and dropped connections.
 * Reconnects with exponential backoff indefinitely (the log stream never ends).
 */
export function useLogsSocket(enabled = true): LogsStream {
  const [logs, setLogs] = useState<ServerLogEntry[]>([]);
  const [status, setStatus] = useState<SocketStatus>("closed");

  // Refs let the reconnect closure read current values without re-subscribing.
  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<number | null>(null);
  const attemptRef = useRef(0);
  // Highest entry id we've applied — used as afterId on reconnect.
  const lastIdRef = useRef(0);

  useEffect(() => {
    if (!enabled) {
      setStatus("closed");
      return;
    }

    let cancelled = false;
    attemptRef.current = 0;

    const clearReconnect = () => {
      if (reconnectTimer.current != null) {
        window.clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
    };

    const scheduleReconnect = () => {
      if (cancelled) return;
      const attempt = attemptRef.current++;
      // 0.5s, 1s, 2s, 4s ... capped at 15s.
      const delay = Math.min(500 * 2 ** attempt, 15_000);
      setStatus("reconnecting");
      reconnectTimer.current = window.setTimeout(connect, delay);
    };

    const applyEntry = (line: ServerLogEntry) => {
      if (line.id > lastIdRef.current) lastIdRef.current = line.id;
    };

    const connect = () => {
      if (cancelled) return;
      setStatus((s) => (s === "reconnecting" ? s : "connecting"));

      const ws = new WebSocket(wsUrl(lastIdRef.current));
      socketRef.current = ws;

      ws.onopen = () => {
        if (cancelled) return;
        attemptRef.current = 0;
        setStatus("open");
      };

      ws.onmessage = (event) => {
        if (cancelled) return;
        let msg: ServerLogSocketMessage;
        try {
          msg = JSON.parse(event.data as string) as ServerLogSocketMessage;
        } catch {
          return;
        }

        switch (msg.type) {
          case "snapshot":
            msg.logs.forEach(applyEntry);
            setLogs((prev) => mergeLogs(prev, msg.logs));
            break;
          case "log":
            applyEntry(msg.log);
            setLogs((prev) => mergeLogs(prev, [msg.log]));
            break;
        }
      };

      ws.onclose = () => {
        if (cancelled) return;
        socketRef.current = null;
        scheduleReconnect();
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
  }, [enabled]);

  return {
    logs,
    status,
    clear: () => setLogs([]),
  };
}
