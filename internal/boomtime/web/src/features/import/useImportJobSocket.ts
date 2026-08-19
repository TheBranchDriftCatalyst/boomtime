import { useEffect, useState } from "react";
import {
  capLines,
  useDurableSocket,
  type SocketStatus,
} from "@/hooks/useDurableSocket";
import type {
  ImportJob,
  ImportLogLine,
  ImportSocketMessage,
} from "@/types/api";
import { isTerminalState } from "@/types/api";

export type { SocketStatus } from "@/hooks/useDurableSocket";

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

/**
 * Subscribes to a durable import job's live stream over WebSocket.
 *
 * The HttpOnly refresh_token cookie authenticates the handshake automatically
 * (same-origin), so no token is placed in the URL. On (re)connect the server
 * sends a fresh `snapshot`, which fully replaces local state — making reloads
 * and dropped connections resume seamlessly. Auto-reconnects with exponential
 * backoff (see useDurableSocket) until the job reaches a terminal state.
 */
export function useImportJobSocket(jobId: number | null): ImportJobStream {
  const [job, setJob] = useState<ImportJob | null>(null);
  const [logs, setLogs] = useState<ImportLogLine[]>([]);

  // Unbinding (jobId -> null) clears the streamed state; a rebind to another
  // job keeps it until that job's snapshot replaces it.
  useEffect(() => {
    if (jobId == null) {
      setJob(null);
      setLogs([]);
    }
  }, [jobId]);

  const status = useDurableSocket<ImportSocketMessage>({
    enabled: jobId != null,
    resetKey: jobId,
    buildUrl: () => wsUrl(jobId as number),
    onMessage: (msg, ctrl) => {
      switch (msg.type) {
        case "snapshot":
          // Full re-sync: replace both job and logs.
          setJob(msg.job);
          setLogs(capLines(msg.logs));
          if (isTerminalState(msg.job.state)) ctrl.preventReconnect();
          break;
        case "log":
          setLogs((prev) => capLines([...prev, msg.log]));
          break;
        case "progress":
          // Merge updated counters; keep the latest job snapshot.
          setJob((prev) => ({ ...(prev ?? msg.job), ...msg.job }));
          break;
        case "state":
          setJob((prev) => ({ ...(prev ?? msg.job), ...msg.job }));
          if (isTerminalState(msg.job.state)) {
            ctrl.preventReconnect();
            ctrl.close();
          }
          break;
      }
    },
  });

  return { job, logs, status };
}
