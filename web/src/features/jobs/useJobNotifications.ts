import { useEffect } from "react";
import { toast } from "sonner";
import { IS_BOOKS_STANDALONE } from "@/lib/standalone";

// Mirrors internal/jobs.JobEvent (only the terminal fields the FE toasts on).
interface JobEvent {
  id: number;
  kind: string;
  owner: string;
  status: "done" | "failed";
  error?: string;
}

// Friendly labels for known kinds; unknown kinds fall back to a de-slugged name.
const KIND_LABELS: Record<string, string> = {
  "avatar-render": "Avatar render",
};

function label(kind: string): string {
  return KIND_LABELS[kind] ?? kind.replace(/-/g, " ");
}

/**
 * useJobNotifications (gaka-hney.6): subscribes to the per-user catalyst-go-jobs
 * event stream (/api/v1/jobs/ws, cookie-authed) and toasts when one of the
 * caller's jobs completes or fails. Mounted once in AppShell. Reconnects on a
 * dropped socket so the stream survives brief blips.
 */
export function useJobNotifications() {
  useEffect(() => {
    // The books-only standalone server doesn't run the jobs event stream
    // (/api/v1/jobs/ws) — don't open a socket that 404s + retries forever.
    if (IS_BOOKS_STANDALONE) return;
    let ws: WebSocket | null = null;
    let closed = false;
    let retry: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      if (closed) return;
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      ws = new WebSocket(`${proto}//${window.location.host}/api/v1/jobs/ws`);

      ws.onmessage = (e) => {
        let ev: JobEvent;
        try {
          ev = JSON.parse(e.data as string);
        } catch {
          return;
        }
        const name = label(ev.kind);
        if (ev.status === "done") {
          toast.success(`${name} complete`);
        } else if (ev.status === "failed") {
          toast.error(`${name} failed`, { description: ev.error || undefined });
        }
      };
      ws.onclose = () => {
        ws = null;
        if (!closed) retry = setTimeout(connect, 5000);
      };
      ws.onerror = () => ws?.close();
    };

    connect();
    return () => {
      closed = true;
      if (retry) clearTimeout(retry);
      ws?.close();
    };
  }, []);
}
