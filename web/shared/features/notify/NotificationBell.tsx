// NotificationBell — the header bell + side panel (gaka-books). Shows an unread
// badge; opening the panel lists notifications (durable, replayed on session start,
// + this session's live ones) and marks them read. Book finishes land here.
import { useState } from "react";
import { Bell, BookMarked, CheckCheck } from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { useNotificationStore } from "@shared/features/notify/NotificationsProvider";

function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const s = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.round(h / 24);
  return `${d}d ago`;
}

// Book-related notifications get a bookmark glyph; everything else the bell.
function iconFor(type: string) {
  return type.startsWith("book") ? BookMarked : Bell;
}

export function NotificationBell() {
  const { notifications, unreadCount, markAllRead } = useNotificationStore();
  const [open, setOpen] = useState(false);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label={
          unreadCount > 0 ? `Notifications, ${unreadCount} unread` : "Notifications"
        }
        className="relative inline-flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Bell className="h-[18px] w-[18px]" />
        {unreadCount > 0 && (
          <span className="absolute -right-0.5 -top-0.5 inline-flex min-w-[1.05rem] items-center justify-center rounded-full bg-primary px-1 text-[10px] font-semibold leading-4 text-primary-foreground">
            {unreadCount > 99 ? "99+" : unreadCount}
          </span>
        )}
      </button>

      <Sheet
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (o && unreadCount > 0) markAllRead();
        }}
      >
        <SheetContent side="right" className="w-[24rem] overflow-y-auto sm:max-w-[24rem]">
          <SheetHeader className="pb-2">
            <SheetTitle className="flex items-center gap-2 text-base">
              <Bell className="h-4 w-4" /> Notifications
            </SheetTitle>
            <SheetDescription className="text-xs">
              Book finishes and activity land here. Durable ones stick across
              sessions.
            </SheetDescription>
          </SheetHeader>

          {notifications.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-16 text-center text-sm text-muted-foreground">
              <Bell className="h-6 w-6 opacity-40" />
              Nothing yet — finish a book and it&apos;ll show up here.
            </div>
          ) : (
            <div className="space-y-1.5">
              {notifications.map((n) => {
                const Icon = iconFor(n.type);
                return (
                  <div
                    key={n.id}
                    className="flex items-start gap-2.5 rounded-lg border border-border/60 px-3 py-2"
                  >
                    <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
                      <Icon className="h-3.5 w-3.5" />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        {!n.read && (
                          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-primary" />
                        )}
                        <span className="truncate text-sm font-medium text-foreground">
                          {n.title}
                        </span>
                      </div>
                      {n.body ? (
                        <div className="truncate text-xs text-muted-foreground">
                          {n.body}
                        </div>
                      ) : null}
                      <div className="mt-0.5 text-[10px] uppercase tracking-wide text-muted-foreground/70">
                        {relativeTime(n.at)}
                      </div>
                    </div>
                  </div>
                );
              })}
              <button
                type="button"
                onClick={markAllRead}
                className="mt-2 inline-flex w-full items-center justify-center gap-1.5 rounded-md border border-border/60 px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                <CheckCheck className="h-3.5 w-3.5" /> Mark all read
              </button>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </>
  );
}
