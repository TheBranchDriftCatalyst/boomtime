// NotificationsProvider — the per-user notification store (gaka-books).
// Subscribes ONCE to the domain-agnostic notify stream (/api/v1/notify/ws), raises
// a sonner toast per event (as before), AND keeps a viewable list surfaced by the
// header bell + panel.
//
// Durability model (mirrors the backend notify.Event.Durable split):
//   - DURABLE events (e.g. book.finished) are written server-side; we REPLAY them
//     on mount via GET /api/v1/notifications, so an event fired while the user had
//     no session open is not dropped on the floor.
//   - EPHEMERAL events are live-only (toast + this session's list); they vanish on
//     reload, which is correct — they were never meant to persist.
// Read state is server-authoritative for durable rows (POST …/read).
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { toast } from "sonner";
import { api } from "@shared/lib/api";
import { IS_BOOKS_STANDALONE } from "@shared/lib/standalone";

// Mirrors internal/notify.Event (Durable is server-only, not on the WS wire).
export interface NotifyEvent {
  type: string;
  owner: string;
  title: string;
  body?: string;
  data?: Record<string, unknown>;
  at?: string;
}

export interface StoredNotification {
  id: string; // "s<dbid>" for durable, "l<seq>" for a live ephemeral entry
  type: string;
  title: string;
  body?: string;
  data?: Record<string, unknown>;
  at: string; // ISO timestamp
  read: boolean;
}

interface NotificationsContextValue {
  notifications: StoredNotification[];
  unreadCount: number;
  markAllRead: () => void;
  refresh: () => void;
}

const NotificationsContext = createContext<NotificationsContextValue | null>(null);
const MAX = 50;

export function NotificationsProvider({ children }: { children: React.ReactNode }) {
  const [notifications, setNotifications] = useState<StoredNotification[]>([]);
  const seq = useRef(0);

  // Replay durable notifications from the server (session-start delivery).
  const refresh = useCallback(async () => {
    // The books-only standalone server serves neither the notify stream nor the
    // durable-notifications endpoint — skip the replay so it doesn't 404.
    if (IS_BOOKS_STANDALONE) return;
    try {
      const res = await api.getNotifications();
      const durable = (res.notifications ?? []).map<StoredNotification>((n) => ({
        id: `s${n.id}`,
        type: n.type,
        title: n.title,
        body: n.body,
        data: n.data,
        at: n.at,
        read: n.readAt != null,
      }));
      setNotifications((prev) => {
        // Keep this session's live-only (ephemeral) entries; replace the durable
        // set with the server's authoritative rows.
        const live = prev.filter((n) => n.id.startsWith("l"));
        return [...durable, ...live]
          .sort((a, b) => (a.at < b.at ? 1 : -1))
          .slice(0, MAX);
      });
    } catch {
      // Offline / unauth — the live stream still works this session.
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // The single WS subscription (live delivery). Reconnects on a dropped socket.
  useEffect(() => {
    // No notify WS on the books-only standalone server — don't open a socket
    // that would fail + retry every 5s and spam the console.
    if (IS_BOOKS_STANDALONE) return;
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
        const stored: StoredNotification = {
          id: `l${seq.current++}`,
          type: ev.type,
          title: ev.title,
          body: ev.body,
          data: ev.data,
          at: ev.at ?? new Date().toISOString(),
          read: false,
        };
        setNotifications((prev) => [stored, ...prev].slice(0, MAX));
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

  const markAllRead = useCallback(() => {
    setNotifications((prev) =>
      prev.some((n) => !n.read) ? prev.map((n) => ({ ...n, read: true })) : prev,
    );
    // Durable rows are server-authoritative — persist the read flip (best-effort).
    // The standalone books server has no such endpoint, so skip the POST.
    if (!IS_BOOKS_STANDALONE) void api.markNotificationsRead().catch(() => {});
  }, []);

  const unreadCount = notifications.reduce((n, x) => n + (x.read ? 0 : 1), 0);

  const value = useMemo(
    () => ({ notifications, unreadCount, markAllRead, refresh }),
    [notifications, unreadCount, markAllRead, refresh],
  );
  return (
    <NotificationsContext.Provider value={value}>
      {children}
    </NotificationsContext.Provider>
  );
}

export function useNotificationStore(): NotificationsContextValue {
  const ctx = useContext(NotificationsContext);
  if (!ctx) {
    throw new Error(
      "useNotificationStore must be used within a NotificationsProvider",
    );
  }
  return ctx;
}
