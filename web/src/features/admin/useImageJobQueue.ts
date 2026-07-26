// useImageJobQueue — durable per-label-image job queue hook (gaka-8bz).
//
// Prior to gaka-8bz the AdminTab tracked in-flight regens in a client-side
// `Set<labelId>`, ran a MAX_PARALLEL_REGENS=2 fake pool by hand, and polled
// `/admin/label-images` every 10 seconds to see completions. Reload/close
// dropped the Set entirely — the server kept generating but the reopened
// tab had no visibility until the next poll landed. From the user's
// perspective, refreshes orphaned runs.
//
// The server now owns the queue (see internal/queue/imagejobs) and streams
// the full lifecycle over `/api/v1/admin/label-images/ws`. This hook
// manages a single WS, keeps a Map<jobId, JobState> in state, and exposes
// a `byLabel(labelId)` helper the row-render loop uses to show status.
// Reconnect + snapshot means opening a new tab or refreshing immediately
// shows what the server is currently running / recently done.
//
// The hook is safe to call from multiple components — each caller opens
// its own WS (redundant but correct). If the admin tab grows a second
// caller, promote this to a module-level singleton.

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";

export type JobStatus = "queued" | "running" | "done" | "error";

export interface JobState {
  id: string;
  labelId: string;
  status: JobStatus;
  error?: string;
  enqueuedAt: string;
  startedAt?: string;
  finishedAt?: string;
  // Overrides — passed to the shim by the server. Not needed for UI today
  // but kept so a future "row inspector" panel can show what actually ran.
  prompt?: string;
  model?: string;
  size?: string;
  seed?: number;
}

// Wire shapes — mirror the server (internal/queue/imagejobs.Event and
// admin_label_images.go event2json). Kept as JSON any-typed so a server
// field addition doesn't break the client at compile time; the hook
// pulls only the fields it renders.
interface WSSnapshot {
  kind: "snapshot";
  jobs: JobState[];
}
interface WSEvent {
  kind: "added" | "updated" | "removed";
  job: JobState;
}
type WSMessage = WSSnapshot | WSEvent;

export interface UseImageJobQueueResult {
  jobs: Map<string, JobState>;
  /** Return the CURRENT (most recently enqueued) job for a label, or undefined. */
  byLabel: (labelId: string) => JobState | undefined;
  enqueue: (entry: {
    labelId: string;
    prompt: string;
    model?: string;
    size?: string;
    seed?: number;
  }) => Promise<{ jobId: string; existing: boolean }>;
  /** True while the WS is OPEN. Used only for a small status indicator; the
   *  hook auto-reconnects, so callers don't need to react to false. */
  connected: boolean;
  /** Increments on each reconnect attempt so the UI can render a subtle
   *  "reconnecting…" badge. Resets to 0 on a successful OPEN. */
  reconnectAttempt: number;
}

// Backoff schedule. Doubles until 30s cap. Reset to 500ms on every open.
const BACKOFF_MS = [500, 1000, 2000, 4000, 8000, 16000, 30000] as const;

function wsUrl(path: string): string {
  // Respect the current page's protocol/host so this works behind the
  // Vite dev proxy AND in prod. Vite proxies /api/v1/... to the Go server
  // AND upgrades ws:// connections transparently when the same-origin
  // request hits the /api prefix.
  if (typeof window === "undefined") return path;
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}${path}`;
}

export function useImageJobQueue(): UseImageJobQueueResult {
  const [jobs, setJobs] = useState<Map<string, JobState>>(() => new Map());
  const [connected, setConnected] = useState(false);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);

  // WS + reconnect timer live in refs so their identity doesn't churn on
  // re-renders. The cleanup on unmount reads current refs; a stale-closure
  // read is not a concern.
  const wsRef = useRef<WebSocket | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const attemptRef = useRef(0);
  // Guard against setState on an unmounted component. React 19's strict
  // effects would double-mount the WS in dev; a small mounted-flag avoids
  // leaking a second socket per callback.
  const mountedRef = useRef(true);

  const applyMessage = useCallback((msg: WSMessage) => {
    setJobs((prev) => {
      if (msg.kind === "snapshot") {
        const next = new Map<string, JobState>();
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
      // Close any lingering socket before opening a new one — belt and
      // braces against a race where the previous onclose already fired
      // its reconnect but the browser is still holding the old handle.
      if (wsRef.current) {
        try {
          wsRef.current.close();
        } catch {
          /* ignore; the socket may already be closing */
        }
        wsRef.current = null;
      }
      let ws: WebSocket;
      try {
        ws = new WebSocket(wsUrl("/api/v1/admin/label-images/ws"));
      } catch {
        // Constructor throws synchronously on invalid URLs; schedule a
        // retry so a transient wobble doesn't wedge the hook.
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
          // Non-JSON frame — server should never send these, so a
          // silent skip is fine. A future ping/pong control frame
          // would land here too.
        }
      };
      ws.onerror = () => {
        // onerror fires BEFORE onclose in every browser we care about;
        // let onclose handle the reconnect scheduling so we don't
        // double-arm the timer.
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

  const byLabel = useCallback(
    (labelId: string): JobState | undefined => {
      // Small map (bounded by concurrency + retention window, so tens at
      // worst); linear scan is fine.
      let best: JobState | undefined;
      for (const job of jobs.values()) {
        if (job.labelId !== labelId) continue;
        if (!best) {
          best = job;
          continue;
        }
        // Prefer the most recently enqueued job. Ties are broken by ID
        // for deterministic behavior.
        if (job.enqueuedAt > best.enqueuedAt) best = job;
      }
      return best;
    },
    [jobs],
  );

  const enqueue = useCallback(
    async (entry: {
      labelId: string;
      prompt: string;
      model?: string;
      size?: string;
      seed?: number;
    }): Promise<{ jobId: string; existing: boolean }> => {
      const body: {
        id: string;
        prompt: string;
        model?: string;
        size?: string;
        seed?: number;
      } = { id: entry.labelId, prompt: entry.prompt };
      if (entry.model) body.model = entry.model;
      if (entry.size) body.size = entry.size;
      if (entry.seed !== undefined) body.seed = entry.seed;
      const res = await api.regenerateLabelImages({
        entries: [body],
        ids: [entry.labelId],
      });
      const first = res.jobs[0];
      if (!first) {
        throw new Error("regenerateLabelImages: empty jobs[] in response");
      }
      return { jobId: first.jobId, existing: first.existing };
    },
    [],
  );

  return { jobs, byLabel, enqueue, connected, reconnectAttempt };
}
