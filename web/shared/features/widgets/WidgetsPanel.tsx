import { useCallback, useEffect, useRef, useState } from "react";
import { LayoutGrid } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import type { WidgetScope } from "@shared/types/api";
import { embeddableCatalogFor } from "./catalog";
import { useWidgetLink } from "./useWidgetLink";
import { WidgetBuilder } from "./WidgetBuilder";
import { WidgetCard } from "./WidgetCard";

interface WidgetsPanelProps {
  /** Page scope: Overview = user, project detail = project, Space = space. */
  scopeType: WidgetScope;
  /** Project name or space id; empty for user scope. */
  scopeRef?: string;
}

const RANGE_CHOICES = [7, 30, 90, 366] as const;

// Sheet width envelope. Default fits catalyst-ui's SheetContent right side;
// user can drag the west-edge handle within [MIN_W, MAX_W] to widen for the
// bigger widget previews or shrink to reclaim real estate. Persisted so it
// survives navigation + reload.
const DEFAULT_WIDTH = 480;
const MIN_WIDTH = 360;
const MAX_WIDTH = 960;
const WIDTH_STORAGE_KEY = "boomtime.widgets-panel.width";

function loadWidth(): number {
  if (typeof window === "undefined") return DEFAULT_WIDTH;
  const raw = window.localStorage.getItem(WIDTH_STORAGE_KEY);
  if (!raw) return DEFAULT_WIDTH;
  const n = Number(raw);
  if (!Number.isFinite(n)) return DEFAULT_WIDTH;
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, n));
}

// The Widgets side panel — the discovery/front-door UX for embeddable
// widgets. Opens from the page toolbar; lists the scope's catalog with live
// previews and copyable embed snippets. The widget link is minted lazily on
// first open (the Sheet mounts its content on open).
//
// Layout: SheetContent is forced into a flex column so the header stays
// pinned at the top and the scroll region below it takes the remaining
// height. Without this, catalyst-ui's default SheetContent lets the tall
// widget catalog overflow the viewport and the copy-snippet controls at
// the bottom become unreachable (the specific bug the user hit).
//
// Resize: an invisible 4px column pinned to the sheet's LEFT edge is the
// drag handle. Mouse-down starts a document-level pointermove listener
// that updates width in-place (bypasses React re-render for smoothness),
// releasing pointermove commits to localStorage. Escape or pointerup
// cancels/settles.
export function WidgetsPanel({ scopeType, scopeRef = "" }: WidgetsPanelProps) {
  const [open, setOpen] = useState(false);
  const [days, setDays] = useState<number>(30);
  const [theme, setTheme] = useState<string>("dark");
  const [width, setWidth] = useState<number>(() => loadWidth());
  const link = useWidgetLink(scopeType, scopeRef, open);
  const draggingRef = useRef(false);

  // Only backend-SVG-renderable kinds (embeddableCatalogFor filters out the
  // FE-only / dashboard-only catalog entries that would 404 as an SVG embed
  // and render as empty widget cards).
  const entries = embeddableCatalogFor(scopeType);

  // Persist width whenever it settles (not on every drag frame).
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(WIDTH_STORAGE_KEY, String(width));
  }, [width]);

  const onDragStart = useCallback((e: React.PointerEvent) => {
    e.preventDefault();
    draggingRef.current = true;
    // Force pointer capture on the handle so a fast drag doesn't fall off
    // the 4px column mid-motion.
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    const startX = e.clientX;
    const startWidth = width;
    const onMove = (ev: PointerEvent) => {
      if (!draggingRef.current) return;
      // Sheet is anchored right; dragging LEFT (dx<0) widens it.
      const dx = startX - ev.clientX;
      const next = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, startWidth + dx));
      setWidth(next);
    };
    const onUp = () => {
      draggingRef.current = false;
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
  }, [width]);

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="outline" size="sm" aria-label="Open widgets panel">
          <LayoutGrid className="mr-1.5 h-4 w-4" />
          Widgets
        </Button>
      </SheetTrigger>
      <SheetContent
        className="flex flex-col p-0 sm:max-w-none"
        style={{ width: `${width}px`, maxWidth: `${MAX_WIDTH}px` }}
      >
        {/* Drag handle — 4px column on the west edge. Broadens hit area to
            8px via a negative-margin overlay for easier grabbing without
            visually widening the column. Vertical center indicator gives
            operators a visible cue. */}
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize widgets panel"
          onPointerDown={onDragStart}
          className="absolute left-0 top-0 bottom-0 w-1 cursor-ew-resize hover:bg-primary/30 active:bg-primary/50 transition-colors group"
          style={{ touchAction: "none" }}
        >
          <div className="absolute left-0 top-1/2 -translate-y-1/2 h-12 w-1 rounded-r bg-border group-hover:bg-primary transition-colors" />
        </div>

        <SheetHeader className="p-6 pb-4 border-b border-border/40">
          <SheetTitle>Embeddable widgets</SheetTitle>
          <SheetDescription>
            Live SVG cards for your GitHub README, blog or site. Copy a snippet
            — it stays up to date. (iframes don&apos;t work in GitHub READMEs;
            use Markdown or the image URL there.)
          </SheetDescription>
        </SheetHeader>

        {/* Scrollable body. min-h-0 is required inside a flex column so the
            child's overflow works — without it flex-1 expands to natural
            content height and no scroll bar appears. */}
        <div className="flex-1 min-h-0 overflow-y-auto p-6 space-y-4">
          <div>
            <WidgetBuilder scopeType={scopeType} scopeRef={scopeRef} />
          </div>

          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1">
              {RANGE_CHOICES.map((d) => (
                <Button
                  key={d}
                  variant={days === d ? "default" : "outline"}
                  size="sm"
                  onClick={() => setDays(d)}
                >
                  {d === 366 ? "1y" : `${d}d`}
                </Button>
              ))}
            </div>
            <div className="flex items-center gap-1">
              {(["dark", "light"] as const).map((t) => (
                <Button
                  key={t}
                  variant={theme === t ? "default" : "outline"}
                  size="sm"
                  onClick={() => setTheme(t)}
                >
                  {t}
                </Button>
              ))}
            </div>
          </div>

          {link.isLoading && (
            <div className="text-sm text-muted-foreground">Minting link…</div>
          )}
          {link.isError && (
            <div className="text-sm text-destructive">
              Could not create the widget link.
            </div>
          )}
          {link.data && (
            <div className="space-y-4">
              {entries.map((entry) => (
                <WidgetCard
                  key={entry.kind}
                  entry={entry}
                  baseUrl={link.data.widgetBaseUrl}
                  days={days}
                  theme={theme}
                />
              ))}
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
