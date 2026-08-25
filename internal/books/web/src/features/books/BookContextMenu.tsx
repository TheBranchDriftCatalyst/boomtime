// BookContextMenu — right-click actions on a book row (boom-w20s).
//
// Built as a small positioned popover rather than with a Radix ContextMenu
// because @radix-ui/react-context-menu is not a dependency here (only
// react-dropdown-menu is), and pulling one in for a handful of items is not
// worth the bundle. The explorer supplies the row and the click event; this
// renders at the cursor and closes on outside-click, Escape, scroll, or resize.
import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Download, ExternalLink, Loader2, RotateCw, Trash2 } from "lucide-react";
import { api } from "@shared/lib/api";
import type { ReadingItemDTO } from "@shared/types/meta";
import { openHardcover } from "@books/features/books/hardcover";
import { useLiberationAvailable } from "@books/features/books/LiberationPanel";

export interface ContextMenuState {
  row: ReadingItemDTO;
  x: number;
  y: number;
}

/** useBookContextMenu owns the open/position state; pass onRowContextMenu to the explorer. */
export function useBookContextMenu() {
  const [menu, setMenu] = useState<ContextMenuState | null>(null);
  const onRowContextMenu = useCallback((row: ReadingItemDTO, e: React.MouseEvent) => {
    setMenu({ row, x: e.clientX, y: e.clientY });
  }, []);
  const close = useCallback(() => setMenu(null), []);
  return { menu, onRowContextMenu, close };
}

const IN_FLIGHT = new Set(["licensing", "downloading", "converting"]);

export function BookContextMenu({
  menu,
  onClose,
  onOpenDetails,
}: {
  menu: ContextMenuState | null;
  onClose: () => void;
  onOpenDetails: (row: ReadingItemDTO) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const qc = useQueryClient();
  const { available } = useLiberationAvailable();
  const [banner, setBanner] = useState<string | null>(null);

  const liberate = useMutation({
    mutationFn: ({ id, force }: { id: string; force: boolean }) => api.liberateBook(id, force),
    onSuccess: (r) => {
      setBanner(`Queued (job ${r.jobId})`);
      void qc.invalidateQueries({ queryKey: ["liberation"] });
      // Leave the toast visible briefly rather than yanking the menu away, so
      // there is feedback that the click did something.
      setTimeout(onClose, 1200);
    },
    onError: (e: Error) => setBanner(e.message),
  });

  const forget = useMutation({
    mutationFn: (id: string) => api.forgetLiberation(id, true),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["liberation"] });
      onClose();
    },
    onError: (e: Error) => setBanner(e.message),
  });

  // Dismissal. Scroll and resize close too: a menu pinned to viewport
  // coordinates is wrong the moment the page moves under it.
  useEffect(() => {
    if (!menu) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    window.addEventListener("scroll", onClose, true);
    window.addEventListener("resize", onClose);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", onClose, true);
      window.removeEventListener("resize", onClose);
    };
  }, [menu, onClose]);

  useEffect(() => setBanner(null), [menu]);

  if (!menu) return null;
  const { row } = menu;
  const status = row.liberationStatus;
  const inFlight = IN_FLIGHT.has(status ?? "");
  const done = status === "liberated";
  const denied = status === "denied";
  // Liberation is Audible-only — a Kindle ebook has no audiobook to liberate.
  const canLiberate = available && row.source === "audible";
  const busy = liberate.isPending || forget.isPending;

  // Keep the menu on screen when right-clicking near an edge.
  const MENU_W = 232;
  const MENU_H = canLiberate ? 190 : 96;
  const x = Math.min(menu.x, window.innerWidth - MENU_W - 8);
  const y = Math.min(menu.y, window.innerHeight - MENU_H - 8);

  const Item = ({
    icon: Icon,
    label,
    onClick,
    disabled,
    title,
  }: {
    icon: typeof Download;
    label: string;
    onClick: () => void;
    disabled?: boolean;
    title?: string;
  }) => (
    <button
      type="button"
      role="menuitem"
      disabled={disabled}
      title={title}
      onClick={onClick}
      className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted/60 disabled:cursor-not-allowed disabled:opacity-40"
    >
      <Icon className="h-3.5 w-3.5 shrink-0" />
      {label}
    </button>
  );

  return (
    <div
      ref={ref}
      role="menu"
      style={{ position: "fixed", left: x, top: y, width: MENU_W, zIndex: 60 }}
      className="rounded-md border border-border bg-popover p-1 shadow-lg"
    >
      <div className="truncate px-2 py-1 text-[11px] text-muted-foreground" title={row.title}>
        {row.title}
      </div>

      {canLiberate && (
        <>
          <Item
            icon={busy ? Loader2 : done ? RotateCw : Download}
            label={
              busy
                ? "Working…"
                : inFlight
                  ? "Liberation in progress…"
                  : done
                    ? "Re-liberate"
                    : "Liberate"
            }
            disabled={busy || inFlight || denied}
            title={denied ? "Audible refused a license for this title" : undefined}
            onClick={() => liberate.mutate({ id: row.externalId, force: done })}
          />
          {done && (
            <Item
              icon={Trash2}
              label="Delete local file"
              disabled={busy}
              onClick={() => forget.mutate(row.externalId)}
            />
          )}
          <div className="my-1 h-px bg-border" />
        </>
      )}

      <Item icon={BookOpen} label="Open details" onClick={() => { onOpenDetails(row); onClose(); }} />
      <Item
        icon={ExternalLink}
        label="Open on Hardcover"
        disabled={row.hardcoverBookId == null && !row.hardcoverSlug}
        onClick={() => { openHardcover(row); onClose(); }}
      />

      {banner && (
        <p className="border-t border-border px-2 pb-1 pt-1.5 text-[11px] text-muted-foreground">
          {banner}
        </p>
      )}
    </div>
  );
}
