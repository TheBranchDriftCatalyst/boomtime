// useReadingMonitorSocket.ts — subscribes to the admin Kindle reading-monitor
// WebSocket (GET /api/v1/admin/books/reading-monitor/ws) and accumulates its
// frames into the state the Reading-monitor tab renders. Built on the shared
// useDurableSocket lifecycle (auto-reconnect + status), mirroring useLogsSocket.
//
// The HttpOnly refresh_token cookie authenticates the handshake (same-origin),
// so no token is in the URL. `enabled` is the Start/Stop switch: false tears the
// socket down. The server streams `sample` (a first-seen/advanced position),
// `heartbeat` (one per poll, proves liveness), `info` (connect banner), and
// `error` frames.
import { useRef, useState } from "react";
import { useDurableSocket, type SocketStatus } from "@/hooks/useDurableSocket";
import type { RawSample } from "./readingMonitorCadence";

export type { SocketStatus } from "@/hooks/useDurableSocket";

// The discriminated wire union the server emits (see internal/admin/books_monitor.go).
export type ReadingMonitorMessage =
  | { type: "info"; intervalSec?: number; message?: string; sampledAt?: string }
  | {
      type: "sample";
      asin: string;
      title?: string;
      location: number;
      creationTime?: string;
      sampledAt: string;
    }
  | { type: "heartbeat"; books?: number; polled?: number; sampledAt?: string }
  | {
      type: "error";
      asin?: string;
      title?: string;
      error: string;
      sampledAt?: string;
    };

export interface HeartbeatState {
  books: number;
  polled: number;
  sampledAt: string;
}

export interface ReadingMonitorStream {
  /** Raw samples in arrival (chronological) order; capped. */
  samples: RawSample[];
  /** The most recent heartbeat, or null before the first poll completes. */
  lastHeartbeat: HeartbeatState | null;
  /** The connect banner message from the `info` frame. */
  info: string | null;
  /** Recent error-frame messages (newest last), capped. */
  errors: string[];
  status: SocketStatus;
  /** Clear the local buffers (does not affect the server). */
  clear: () => void;
}

export interface ReadingMonitorOptions {
  enabled: boolean;
  intervalSec?: number;
  limit?: number;
}

// Cap the sample buffer so a long session can't grow unbounded.
const MAX_SAMPLES = 1000;
const MAX_ERRORS = 50;

function wsUrl(intervalSec?: number, limit?: number): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const qs = new URLSearchParams();
  if (intervalSec) qs.set("interval", String(intervalSec));
  if (limit) qs.set("limit", String(limit));
  const suffix = qs.toString() ? `?${qs}` : "";
  return `${proto}//${window.location.host}/api/v1/admin/books/reading-monitor/ws${suffix}`;
}

export function useReadingMonitorSocket({
  enabled,
  intervalSec,
  limit,
}: ReadingMonitorOptions): ReadingMonitorStream {
  const [samples, setSamples] = useState<RawSample[]>([]);
  const [lastHeartbeat, setLastHeartbeat] = useState<HeartbeatState | null>(
    null,
  );
  const [info, setInfo] = useState<string | null>(null);
  const [errors, setErrors] = useState<string[]>([]);

  // Reconnect from scratch when the tuning changes (a new interval/limit is a
  // different monitor run).
  const resetKey = `${intervalSec ?? ""}:${limit ?? ""}`;
  const urlRef = useRef({ intervalSec, limit });
  urlRef.current = { intervalSec, limit };

  const status = useDurableSocket<ReadingMonitorMessage>({
    enabled,
    resetKey,
    buildUrl: () => wsUrl(urlRef.current.intervalSec, urlRef.current.limit),
    onMessage: (msg) => {
      switch (msg.type) {
        case "info":
          setInfo(msg.message ?? "monitor live");
          break;
        case "sample":
          setSamples((prev) => {
            const next = [
              ...prev,
              {
                asin: msg.asin,
                title: msg.title ?? msg.asin,
                location: msg.location,
                creationTime: msg.creationTime,
                sampledAt: msg.sampledAt,
              },
            ];
            return next.length > MAX_SAMPLES
              ? next.slice(next.length - MAX_SAMPLES)
              : next;
          });
          break;
        case "heartbeat":
          setLastHeartbeat({
            books: msg.books ?? 0,
            polled: msg.polled ?? 0,
            sampledAt: msg.sampledAt ?? new Date().toISOString(),
          });
          break;
        case "error":
          setErrors((prev) => {
            const label = msg.title ? `${msg.title}: ${msg.error}` : msg.error;
            const next = [...prev, label];
            return next.length > MAX_ERRORS
              ? next.slice(next.length - MAX_ERRORS)
              : next;
          });
          break;
      }
    },
  });

  return {
    samples,
    lastHeartbeat,
    info,
    errors,
    status,
    clear: () => {
      setSamples([]);
      setLastHeartbeat(null);
      setInfo(null);
      setErrors([]);
    },
  };
}
