import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { Command } from "cmdk";
import {
  Award,
  BookOpen,
  Download,
  HeartPulse,
  KeyRound,
  LayoutDashboard,
  Library,
  ListTree,
  LogOut,
  Moon,
  Plus,
  Settings2,
  Shapes,
  Sun,
  Target,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { useTheme } from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";
import { useSpaces } from "@/features/spaces/useSpaces";
import { IS_BOOKS_STANDALONE } from "@/lib/standalone";

// Global open event — a header/mobile search button dispatches this so the
// palette stays decoupled from its triggers (it also opens on ⌘K / Ctrl-K).
const OPEN_EVENT = "boomtime:open-command-palette";

/** Fire from anywhere (e.g. the header search button) to open the palette. */
export function openCommandPalette() {
  window.dispatchEvent(new Event(OPEN_EVENT));
}

// Nav destinations — mirrors the sidebar NAV. Small + stable, so kept inline
// rather than shared (the sidebar owns its own copy for its distinct styling).
// The books-only standalone lists ONLY its reachable pages (the code-domain
// routes aren't mounted there), so ⌘K never jumps to a dead route.
const HOST_PAGES: { name: string; icon: typeof LayoutDashboard; to: string }[] = [
  { name: "Overview", icon: LayoutDashboard, to: "/app" },
  { name: "Projects", icon: BookOpen, to: "/app/projects" },
  { name: "Leaderboards", icon: Award, to: "/app/leaderboards" },
  { name: "Goals", icon: Target, to: "/app/goals" },
  { name: "Heartbeats", icon: ListTree, to: "/app/heartbeats" },
  { name: "Wellness", icon: HeartPulse, to: "/app/wellness" },
  { name: "Catalog", icon: Shapes, to: "/app/catalog" },
  { name: "Import", icon: Download, to: "/app/import" },
  { name: "Settings", icon: Settings2, to: "/app/settings" },
];
const BOOKS_STANDALONE_PAGES: typeof HOST_PAGES = [
  { name: "Books", icon: Library, to: "/app/books" },
  { name: "Settings", icon: Settings2, to: "/app/settings" },
];
const PAGES = IS_BOOKS_STANDALONE ? BOOKS_STANDALONE_PAGES : HOST_PAGES;

interface CommandPaletteProps {
  onCreateSpace: () => void;
  onLogout: () => void;
}

/** ⌘K command palette (gaka-gbbl.1): fuzzy jump to any page / space + quick
 * actions. Built on cmdk (keyboard nav + a11y + filtering) inside the app
 * Dialog. Mounted once in AppShell; opens on ⌘K/Ctrl-K or via
 * openCommandPalette(). */
export function CommandPalette({ onCreateSpace, onLogout }: CommandPaletteProps) {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const { variant, setVariant } = useTheme();
  const { data: spaces } = useSpaces();
  const isDark = variant === "dark";

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    const onOpen = () => setOpen(true);
    window.addEventListener("keydown", onKey);
    window.addEventListener(OPEN_EVENT, onOpen);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener(OPEN_EVENT, onOpen);
    };
  }, []);

  // Close first, then run — so a navigation/dialog-open isn't racing the
  // palette's own unmount.
  const run = (fn: () => void) => {
    setOpen(false);
    fn();
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="overflow-hidden p-0 sm:max-w-lg">
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <Command
          className="flex flex-col [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:pt-2 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-wide [&_[cmdk-group-heading]]:text-muted-foreground"
          loop
        >
          <Command.Input
            placeholder="Search pages, spaces, actions…"
            className="w-full border-b border-border bg-transparent px-4 py-3 text-sm outline-none placeholder:text-muted-foreground"
          />
          <Command.List className="max-h-[min(60vh,420px)] overflow-y-auto p-2">
            <Command.Empty className="py-6 text-center text-sm text-muted-foreground">
              No results.
            </Command.Empty>

            <Command.Group heading="Navigation">
              {PAGES.map((p) => (
                <Item key={p.to} value={p.name} onSelect={() => run(() => navigate(p.to))}>
                  <p.icon className="h-4 w-4 shrink-0 text-muted-foreground" />
                  {p.name}
                </Item>
              ))}
            </Command.Group>

            {(spaces ?? []).length > 0 && (
              <Command.Group heading="Spaces">
                {(spaces ?? []).map((s) => (
                  <Item
                    key={s.id}
                    value={`space ${s.name}`}
                    onSelect={() => run(() => navigate(`/app/space/${s.id}`))}
                  >
                    <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-sm bg-secondary text-[10px] font-semibold text-secondary-foreground">
                      {s.name.trim().charAt(0).toUpperCase() || "S"}
                    </span>
                    {s.name}
                  </Item>
                ))}
              </Command.Group>
            )}

            <Command.Group heading="Actions">
              {/* "New space" is a host-only (code-domain) action — the books
                  standalone has no Spaces. */}
              {!IS_BOOKS_STANDALONE && (
                <Item value="new space create" onSelect={() => run(onCreateSpace)}>
                  <Plus className="h-4 w-4 shrink-0 text-muted-foreground" />
                  New space
                </Item>
              )}
              <Item
                value="api tokens keys"
                onSelect={() => run(() => navigate("/app/settings?tab=tokens"))}
              >
                <KeyRound className="h-4 w-4 shrink-0 text-muted-foreground" />
                API tokens
              </Item>
              <Item
                value={isDark ? "light mode theme" : "dark mode theme"}
                onSelect={() => run(() => setVariant(isDark ? "light" : "dark"))}
              >
                {isDark ? (
                  <Sun className="h-4 w-4 shrink-0 text-muted-foreground" />
                ) : (
                  <Moon className="h-4 w-4 shrink-0 text-muted-foreground" />
                )}
                {isDark ? "Light mode" : "Dark mode"}
              </Item>
              <Item value="logout sign out" onSelect={() => run(onLogout)}>
                <LogOut className="h-4 w-4 shrink-0 text-muted-foreground" />
                Logout
              </Item>
            </Command.Group>
          </Command.List>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

// Item — shared cmdk item styling (keyboard-selected row highlights via
// cmdk's data-[selected=true]).
function Item({
  value,
  onSelect,
  children,
}: {
  value: string;
  onSelect: () => void;
  children: React.ReactNode;
}) {
  return (
    <Command.Item
      value={value}
      onSelect={onSelect}
      className="flex cursor-pointer items-center gap-2 rounded-md px-3 py-2 text-sm text-foreground outline-none data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground"
    >
      {children}
    </Command.Item>
  );
}
