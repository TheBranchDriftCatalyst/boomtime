import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { openCommandPalette } from "@shared/components/CommandPalette";

// "Go to" destinations: press `g` then one of these keys (boom-gbbl.3).
const NAV_KEYS: Record<string, { to: string; label: string }> = {
  o: { to: "/app", label: "Overview" },
  p: { to: "/app/projects", label: "Projects" },
  l: { to: "/app/leaderboards", label: "Leaderboards" },
  g: { to: "/app/goals", label: "Goals" },
  h: { to: "/app/heartbeats", label: "Heartbeats" },
  w: { to: "/app/wellness", label: "Wellness" },
  c: { to: "/app/catalog", label: "Catalog" },
  i: { to: "/app/import", label: "Import" },
  s: { to: "/app/settings", label: "Settings" },
  r: { to: "/app/profile", label: "Profile" },
};

// Don't hijack keystrokes while the user is typing.
function isTypingTarget(el: EventTarget | null): boolean {
  const t = el as HTMLElement | null;
  if (!t) return false;
  return (
    t.tagName === "INPUT" ||
    t.tagName === "TEXTAREA" ||
    t.tagName === "SELECT" ||
    t.isContentEditable
  );
}

/** Global keyboard shortcuts (boom-gbbl.3): a Gmail-style `g`-then-key nav, `?`
 * for the cheatsheet, `/` to open the command palette. Mounted once in
 * AppShell; owns the help dialog. ⌘K is handled by the palette itself. */
export function KeyboardShortcuts() {
  const [helpOpen, setHelpOpen] = useState(false);
  const navigate = useNavigate();
  const gPending = useRef(false);
  const gTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    const clearG = () => {
      gPending.current = false;
      if (gTimer.current) clearTimeout(gTimer.current);
    };
    const onKey = (e: KeyboardEvent) => {
      // Leave modified chords (⌘K, Ctrl-C, …) alone.
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isTypingTarget(e.target)) return;

      // Second key of a `g …` sequence.
      if (gPending.current) {
        const dest = NAV_KEYS[e.key.toLowerCase()];
        clearG();
        if (dest) {
          e.preventDefault();
          navigate(dest.to);
        }
        return;
      }
      if (e.key.toLowerCase() === "g") {
        gPending.current = true;
        gTimer.current = setTimeout(clearG, 1200);
        return;
      }
      if (e.key === "?") {
        e.preventDefault();
        setHelpOpen(true);
        return;
      }
      if (e.key === "/") {
        e.preventDefault();
        openCommandPalette();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      clearG();
    };
  }, [navigate]);

  return (
    <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
        </DialogHeader>
        <div className="space-y-5 pt-1 text-sm">
          <Section title="General">
            <Row keys={["⌘", "K"]} desc="Command palette" />
            <Row keys={["/"]} desc="Command palette" />
            <Row keys={["?"]} desc="This help" />
          </Section>
          <Section title="Go to — press g, then…">
            {Object.entries(NAV_KEYS).map(([key, { label }]) => (
              <Row key={key} keys={["g", key]} desc={label} />
            ))}
          </Section>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </div>
      <div className="space-y-1.5">{children}</div>
    </div>
  );
}

function Row({ keys, desc }: { keys: string[]; desc: string }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-foreground">{desc}</span>
      <span className="flex items-center gap-1">
        {keys.map((k, i) => (
          <kbd
            key={i}
            className="inline-flex min-w-[1.4rem] items-center justify-center rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] leading-none text-muted-foreground"
          >
            {k}
          </kbd>
        ))}
      </span>
    </div>
  );
}
