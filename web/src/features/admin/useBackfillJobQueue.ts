// useBackfillJobQueue — durable WS-backed backfill job queue hook
// (gaka-vh8). Mirrors useImageJobQueue but for the git-history backfill
// registry.
//
// The server owns the registry (see internal/queue/backfilljobs) and
// streams every added / updated / removed event over
// /api/v1/admin/backfill/ws. This hook keeps a Map<jobId, JobState>
// tied to that stream, reconnects with exponential backoff on drop, and
// exposes the state to the BackfillTab table.

import { useCallback, useEffect, useRef, useState } from "react";

export type BackfillJobStatus = "queued" | "running" | "done" | "error";

export interface BackfillJobState {
  id: string;
  owner: string;
  repoName: string;
  repoPath: string;
  status: BackfillJobStatus;
  error?: string;
  total: number;
  processed: number;
  written: number;
  skipped: number;
  enqueuedAt: string;
  startedAt?: string;
  finishedAt?: string;
}

interface WSSnapshot {
  kind: "snapshot";
  jobs: BackfillJobState[];
}

interface WSEvent {
  kind: "added" | "updated" | "removed";
  job: BackfillJobState;
}

type WSMessage = WSSnapshot | WSEvent;

export interface UseBackfillJobQueueResult {
  jobs: Map<string, BackfillJobState>;
  connected: boolean;
  reconnectAttempt: number;
}

// Same backoff schedule as useImageJobQueue.
const BACKOFF_MS = [500, 1000, 2000, 4000, 8000, 16000, 30000] as const;

function wsUrl(path: string): string {
  if (typeof window === "undefined") return path;
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}${path}`;
}

export function useBackfillJobQueue(): UseBackfillJobQueueResult {
  const [jobs, setJobs] = useState<Map<string, BackfillJobState>>(() => new Map());
  const [connected, setConnected] = useState(false);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);

  const wsRef = useRef<WebSocket | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const attemptRef = useRef(0);
  const mountedRef = useRef(true);

  const applyMessage = useCallback((msg: WSMessage) => {
    setJobs((prev) => {
      if (msg.kind === "snapshot") {
        const next = new Map<string, BackfillJobState>();
        for (const j of msg.jobs ?? []) next.set(j.id, j);
        return next;
      }
      const next = new Map(prev);
      if (msg.kind === "removed") {
        next.delete(msg.job.id);
      } else {
        next.set(msg.job.id, msg.job);
      }
      return next;
    });
  }, []);

  const scheduleReconnect = useCallback(
    (connect: () => void) => {
      if (!mountedRef.current) return;
      const idx = Math.min(attemptRef.current, BACKOFF_MS.length - 1);
      const delay = BACKOFF_MS[idx];
      attemptRef.current += 1;
      setReconnectAttempt(attemptRef.current);
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        if (mountedRef.current) connect();
      }, delay);
    },
    [],
  );

  useEffect(() => {
    mountedRef.current = true;
    const connect = () => {
      if (!mountedRef.current) return;
      if (wsRef.current) {
        try {
          wsRef.current.close();
        } catch {
          /* ignore */
        }
        wsRef.current = null;
      }
      let ws: WebSocket;
      try {
        ws = new WebSocket(wsUrl("/api/v1/admin/backfill/ws"));
      } catch {
        scheduleReconnect(connect);
        return;
      }
      wsRef.current = ws;
      ws.onopen = () => {
        if (!mountedRef.current) return;
        attemptRef.current = 0;
        setReconnectAttempt(0);
        setConnected(true);
      };
      ws.onmessage = (ev) => {
        if (!mountedRef.current) return;
        try {
          const msg = JSON.parse(String(ev.data)) as WSMessage;
          applyMessage(msg);
        } catch {
          // Silent skip — future ping/pong frames land here.
        }
      };
      ws.onerror = () => {
        // Let onclose schedule the reconnect.
      };
      ws.onclose = () => {
        if (!mountedRef.current) return;
        setConnected(false);
        wsRef.current = null;
        scheduleReconnect(connect);
      };
    };
    connect();
    return () => {
      mountedRef.current = false;
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = null;
      if (wsRef.current) {
        try {
          wsRef.current.close();
        } catch {
          /* ignore */
        }
        wsRef.current = null;
      }
    };
  }, [applyMessage, scheduleReconnect]);

  return { jobs, connected, reconnectAttempt };
}
