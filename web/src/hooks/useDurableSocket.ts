import { useEffect, useRef, useState } from "react";

// Cap shared by socket-backed log buffers so terminals never grow unbounded.
export const MAX_LOG_LINES = 2000;

/** Keep at most MAX_LOG_LINES entries, dropping from the head (keep the tail). */
export function capLines<T>(lines: T[]): T[] {
  return lines.length > MAX_LOG_LINES
    ? lines.slice(lines.length - MAX_LOG_LINES)
    : lines;
}

export type SocketStatus = "connecting" | "open" | "reconnecting" | "closed";

/** Handed to onMessage so domain hooks can end the stream (e.g. terminal job state). */
export interface DurableSocketControl {
  /** Stop auto-reconnecting; the next close settles status to "closed". */
  preventReconnect: () => void;
  /** Cancel any pending reconnect and close the current socket. */
  close: () => void;
}

interface DurableSocketOptions<TMessage> {
  /** false → no connection; status stays "closed". */
  enabled: boolean;
  /** Builds the (re)connect URL. Called on every attempt, so it may read refs
   *  (e.g. an afterId cursor) to resume where the last connection left off. */
  buildUrl: () => string;
  /** Handles one parsed server message. Malformed (non-JSON) frames are
   *  silently dropped before this is called. */
  onMessage: (msg: TMessage, ctrl: DurableSocketControl) => void;
  /** Tears the connection down and reconnects from scratch when it changes
   *  (e.g. a new job id). */
  resetKey?: unknown;
}

/**
 * Shared machinery for a durable WebSocket subscription: connect, parse,
 * auto-reconnect with exponential backoff (0.5s doubling, capped at 15s),
 * and teardown. Domain hooks (useLogsSocket / useImportJobSocket) own their
 * message protocol and state; this hook owns the connection lifecycle and
 * reports its status.
 */
export function useDurableSocket<TMessage>(
  options: DurableSocketOptions<TMessage>,
): SocketStatus {
  const { enabled, resetKey } = options;
  const [status, setStatus] = useState<SocketStatus>("closed");

  // Latest callbacks readable from the long-lived connect closure without
  // re-subscribing on every render.
  const buildUrlRef = useRef(options.buildUrl);
  buildUrlRef.current = options.buildUrl;
  const onMessageRef = useRef(options.onMessage);
  onMessageRef.current = options.onMessage;

  const socketRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<number | null>(null);
  const attemptRef = useRef(0);
  // Set via ctrl.preventReconnect() — the stream is done (e.g. terminal job
  // state); the next close must not schedule a reconnect.
  const finishedRef = useRef(false);

  useEffect(() => {
    if (!enabled) {
      setStatus("closed");
      return;
    }

    let cancelled = false;
    finishedRef.current = false;
    attemptRef.current = 0;

    const clearReconnect = () => {
      if (reconnectTimer.current != null) {
        window.clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
    };

    const scheduleReconnect = () => {
      if (cancelled || finishedRef.current) return;
      const attempt = attemptRef.current++;
      // 0.5s, 1s, 2s, 4s ... capped at 15s.
      const delay = Math.min(500 * 2 ** attempt, 15_000);
      setStatus("reconnecting");
      reconnectTimer.current = window.setTimeout(connect, delay);
    };

    const ctrl: DurableSocketControl = {
      preventReconnect: () => {
        finishedRef.current = true;
      },
      close: () => {
        clearReconnect();
        socketRef.current?.close();
      },
    };

    const connect = () => {
      if (cancelled || finishedRef.current) return;
      setStatus((s) => (s === "reconnecting" ? s : "connecting"));

      const ws = new WebSocket(buildUrlRef.current());
      socketRef.current = ws;

      ws.onopen = () => {
        if (cancelled) return;
        attemptRef.current = 0;
        setStatus("open");
        // Servers push a snapshot on connect; nothing to send.
      };

      ws.onmessage = (event) => {
        if (cancelled) return;
        let msg: TMessage;
        try {
          msg = JSON.parse(event.data as string) as TMessage;
        } catch {
          return;
        }
        onMessageRef.current(msg, ctrl);
      };

      ws.onclose = () => {
        if (cancelled) return;
        socketRef.current = null;
        if (finishedRef.current) {
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
  }, [enabled, resetKey]);

  return status;
}
