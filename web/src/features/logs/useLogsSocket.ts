import { useRef, useState } from "react";
import { authStore } from "@/features/auth/auth";
import {
  capLines,
  useDurableSocket,
  type SocketStatus,
} from "@/hooks/useDurableSocket";
import type { ServerLogEntry, ServerLogSocketMessage } from "@/types/api";

export type { SocketStatus } from "@/hooks/useDurableSocket";

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
  return capLines(next);
}

/**
 * Subscribes to the server process's own log stream over WebSocket.
 *
 * On (re)connect the server sends a `snapshot` (ring-buffer backfill after the
 * last-seen id) followed by live `log` entries. Entries are de-duplicated by
 * their monotonic `id`, so reconnecting with an `afterId` resumes seamlessly
 * across reloads and dropped connections. Reconnects with exponential backoff
 * indefinitely (the log stream never ends) — see useDurableSocket.
 */
export function useLogsSocket(enabled = true): LogsStream {
  const [logs, setLogs] = useState<ServerLogEntry[]>([]);
  // Highest entry id we've applied — used as afterId on reconnect.
  const lastIdRef = useRef(0);

  const applyEntry = (line: ServerLogEntry) => {
    if (line.id > lastIdRef.current) lastIdRef.current = line.id;
  };

  const status = useDurableSocket<ServerLogSocketMessage>({
    enabled,
    buildUrl: () => wsUrl(lastIdRef.current),
    onMessage: (msg) => {
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
    },
  });

  return {
    logs,
    status,
    clear: () => setLogs([]),
  };
}
