// useImageJobQueue — per-label-image regen status hook.
//
// History: originally a client-side Set + hand-rolled pool (boom-8bz predecessor);
// then a server-owned queue streamed over `/api/v1/admin/label-images/ws`
// (internal/queue/imagejobs). As of boom-hney Stage 3 the regen path is folded
// onto the generic catalyst-go-jobs DB queue + a KEDA ScaledJob, so this hook
// now POLLS `/api/v1/admin/label-images/status` (latest job per label) instead
// of holding a WebSocket. The public shape is unchanged — `byLabel`, `enqueue`,
// `connected`, `reconnectAttempt` — so AdminTab's render loop is untouched;
// `connected` now means "the last status poll succeeded" and `reconnectAttempt`
// counts consecutive poll failures (drives the same subtle "reconnecting…"
// badge). Polling is adaptive: fast while any job is in flight, slow when idle.

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "@shared/lib/api";

export type JobStatus = "queued" | "running" | "done" | "error";

export interface JobState {
  id: string;
  labelId: string;
  status: JobStatus;
  error?: string;
  enqueuedAt: string;
  startedAt?: string;
  finishedAt?: string;
  // Overrides — reserved for a future "row inspector" panel. The status poll
  // doesn't carry them today (they live in the job payload server-side).
  prompt?: string;
  model?: string;
  size?: string;
  seed?: number;
}

export interface UseImageJobQueueResult {
  jobs: Map<string, JobState>;
  /** The CURRENT job for a label (latest per the status endpoint), or undefined. */
  byLabel: (labelId: string) => JobState | undefined;
  enqueue: (entry: {
    labelId: string;
    prompt: string;
    model?: string;
    size?: string;
    seed?: number;
  }) => Promise<{ jobId: string; existing: boolean }>;
  /** True while the last status poll succeeded (auto-retries; callers needn't react). */
  connected: boolean;
  /** Consecutive poll-failure count; 0 while healthy. Drives a "reconnecting…" badge. */
  reconnectAttempt: number;
}

// Poll cadence: brisk while work is in flight, relaxed when idle. On failure,
// exponential backoff off the active cadence, capped.
const ACTIVE_POLL_MS = 2000;
const IDLE_POLL_MS = 6000;
const MAX_BACKOFF_MS = 30000;

export function useImageJobQueue(): UseImageJobQueueResult {
  const [jobs, setJobs] = useState<Map<string, JobState>>(() => new Map());
  const [connected, setConnected] = useState(false);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);

  // Timer + mounted flag + attempt counter live in refs so their identity
  // doesn't churn re-renders. pollRef lets enqueue() trigger an immediate poll.
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);
  const attemptRef = useRef(0);
  const pollRef = useRef<() => void>(() => {});

  useEffect(() => {
    mountedRef.current = true;

    const schedule = (delay: number) => {
      if (!mountedRef.current) return;
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        if (mountedRef.current) pollRef.current();
      }, delay);
    };

    const poll = async () => {
      if (!mountedRef.current) return;
      try {
        const res = await api.getLabelImageStatus();
        if (!mountedRef.current) return;
        const next = new Map<string, JobState>();
        let active = false;
        for (const j of res.jobs ?? []) {
          if (j.status === "queued" || j.status === "running") active = true;
          next.set(j.labelId, {
            id: j.labelId,
            labelId: j.labelId,
            status: j.status,
            error: j.error,
            enqueuedAt: j.startedAt ?? j.finishedAt ?? new Date().toISOString(),
            startedAt: j.startedAt,
            finishedAt: j.finishedAt,
          });
        }
        setJobs(next);
        if (attemptRef.current !== 0) {
          attemptRef.current = 0;
          setReconnectAttempt(0);
        }
        setConnected(true);
        schedule(active ? ACTIVE_POLL_MS : IDLE_POLL_MS);
      } catch {
        if (!mountedRef.current) return;
        setConnected(false);
        attemptRef.current += 1;
        setReconnectAttempt(attemptRef.current);
        schedule(Math.min(ACTIVE_POLL_MS * 2 ** attemptRef.current, MAX_BACKOFF_MS));
      }
    };
    pollRef.current = poll;
    poll(); // immediate first poll on mount

    return () => {
      mountedRef.current = false;
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = null;
    };
  }, []);

  const byLabel = useCallback(
    (labelId: string): JobState | undefined => jobs.get(labelId),
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
      // Reflect the just-committed job promptly rather than waiting a full cycle.
      if (mountedRef.current) pollRef.current();
      return { jobId: first.jobId, existing: first.existing };
    },
    [],
  );

  return { jobs, byLabel, enqueue, connected, reconnectAttempt };
}
