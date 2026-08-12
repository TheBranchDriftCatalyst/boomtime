import { useEffect } from "react";
import { toast } from "sonner";

// Mirrors internal/notify.Event — the self-describing notification envelope.
interface NotifyEvent {
  type: string;
  owner: string;
  title: string;
  body?: string;
  data?: Record<string, unknown>;
  at?: string;
}

/**
 * useNotifications: subscribes to the per-user domain-agnostic notification
 * stream (/api/v1/notify/ws, cookie-authed) and raises a sonner toast per
 * Event using its Title/Body. Mounted once in AppShell. Reconnects on a dropped
 * socket so the stream survives brief blips. Mirrors useJobNotifications.
 */
export function useNotifications() {
  useEffect(() => {
    let ws: WebSocket | null = null;
    let closed = false;
    let retry: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      if (closed) return;
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      ws = new WebSocket(`${proto}//${window.location.host}/api/v1/notify/ws`);

      ws.onmessage = (e) => {
        let ev: NotifyEvent;
        try {
          ev = JSON.parse(e.data as string);
        } catch {
          return;
        }
        if (!ev.title) return;
        toast(ev.title, { description: ev.body || undefined });
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
