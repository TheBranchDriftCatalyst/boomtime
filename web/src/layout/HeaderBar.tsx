import { useLocation, useNavigate } from "react-router";
import {
  Download,
  KeyRound,
  LogOut,
  Moon,
  Palette,
  Search,
  Settings2,
  Sun,
  User,
} from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import {
  useTheme,
  THEME_REGISTRY,
} from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";
import { UserAvatarImage } from "@/features/publicprofile/UserAvatarImage";
import { useIsAdmin } from "@/features/auth/useIsAdmin";
import { DevModeToggle } from "@/features/devtools";
import { useHeaderSlotNode } from "@/layout/HeaderSlot";
import { MobileNav } from "@/layout/MobileNav";
import { openCommandPalette } from "@/components/CommandPalette";

interface HeaderBarProps {
  username: string;
  onLogout: () => void;
  onCreateSpace: () => void;
}

function greetingFor(hour: number): string {
  if (hour < 5) return "Burning the midnight oil";
  if (hour < 12) return "Good morning";
  if (hour < 18) return "Good afternoon";
  return "Good evening";
}

// Route → header title, keyed by the first path segment under /app, so the
// header always says where you are (gaka-gbbl.3). Pages that hoist a tab strip
// (settings/admin) render the slot instead and never fall back to this.
const PAGE_TITLES: Record<string, string> = {
  "": "Overview",
  projects: "Projects",
  leaderboards: "Leaderboards",
  goals: "Goals",
  heartbeats: "Heartbeats",
  wellness: "Wellness",
  catalog: "Catalog",
  import: "Import",
  settings: "Settings",
  admin: "Admin",
  profile: "Profile",
  space: "Space",
  changelog: "Changelog",
};

function pageTitleFromPath(pathname: string): string {
  const seg = pathname.replace(/^\/app\/?/, "").split("/")[0] ?? "";
  return PAGE_TITLES[seg] ?? "Boomtime";
}

/**
 * Top header — a single consolidated user menu (gaka-lzr). The previous bar had
 * TWO overlapping theme controls (a full ThemeSwitcher + a redundant sun/moon
 * ThemeToggle) plus a bare avatar. Everything now lives in one avatar dropdown:
 * account nav, a Theme submenu (theme / variant / effects), a quick dark-mode
 * toggle, and logout. The left side carries a light quick-look greeting.
 */
export function HeaderBar({ username, onLogout, onCreateSpace }: HeaderBarProps) {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const pageTitle = pageTitleFromPath(pathname);
  const { theme, setTheme, variant, setVariant, effects, updateEffect } =
    useTheme();
  // gaka-1im: admin-only devtools (annotation subsystem + component inspector).
  // Invisible to normal users — the toggle renders nothing unless the current
  // user is an admin.
  const { isAdmin } = useIsAdmin();
  const isDark = variant === "dark";
  const greeting = greetingFor(new Date().getHours());

  // gaka-5jp: a page (settings/admin) may hoist its tab strip up here via
  // useHeaderSlot. When present it takes the header's left/center space in
  // place of the greeting; the right-side controls are untouched.
  const slot = useHeaderSlotNode();

  // Subtle --primary ring + faint neon glow, cohesive with the dossier aesthetic.
  const avatar = (size: number) => (
    <span
      className="inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full"
      style={{
        width: size,
        height: size,
        boxShadow:
          "0 0 0 1px color-mix(in oklab, var(--primary) 55%, transparent), 0 0 12px color-mix(in oklab, var(--primary) 28%, transparent)",
      }}
    >
      <UserAvatarImage
        username={username}
        size={size}
        alt={`${username}'s avatar`}
      />
    </span>
  );

  return (
    <header
      className="flex h-16 items-center justify-between gap-3 border-b bg-card px-6"
      style={{
        // Sharpen the bottom edge with a hair of neon.
        borderBottomColor:
          "color-mix(in oklab, var(--primary) 25%, var(--border))",
        boxShadow: "0 1px 0 0 color-mix(in oklab, var(--primary) 14%, transparent)",
      }}
    >
      {/* Mobile-only hamburger → nav drawer (the desktop rail is hidden < md). */}
      <MobileNav onLogout={onLogout} onCreateSpace={onCreateSpace} />

      {/* Left/center: hoisted page chrome (tab strip) when a page set one,
          else a time-aware greeting with a refined terminal treatment.
          flex-1 min-w-0 gives the slot room; the strip scrolls-x on narrow
          widths (TabNav.css) so the header never wraps. */}
      <div className="flex h-full min-w-0 flex-1 items-center">
        {slot ?? (
          <div
            className="flex items-baseline gap-2"
            style={{
              fontFamily: '"JetBrains Mono", "Chakra Petch", ui-monospace, monospace',
            }}
          >
            <span className="text-xs uppercase tracking-[0.18em] text-muted-foreground">
              {"//"}
            </span>
            <span className="truncate text-sm font-semibold tracking-wide text-foreground">
              {pageTitle}
            </span>
          </div>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-2">
        {/* Command palette (⌘K) trigger — an icon on mobile, a search-field
            affordance with the shortcut hint on wide screens. */}
        <button
          type="button"
          onClick={() => openCommandPalette()}
          aria-label="Open command palette"
          className="inline-flex h-9 items-center gap-2 rounded-lg border border-border/60 bg-muted/40 px-2.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <Search className="h-4 w-4" />
          <span className="hidden lg:inline">Search</span>
          <kbd className="hidden rounded border border-border bg-background px-1.5 font-mono text-[10px] leading-5 lg:inline">
            ⌘K
          </kbd>
        </button>

        {/* Admin-only dev utilities — renders nothing for normal users. */}
        {isAdmin && <DevModeToggle variant="ghost" size="icon" />}

        <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label="Open user menu"
            className="group flex items-center gap-2 rounded-full p-0.5 pr-1 transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
            <span className="hidden text-sm text-muted-foreground sm:inline">
              {username}
            </span>
            {avatar(36)}
          </button>
        </DropdownMenuTrigger>

        <DropdownMenuContent align="end" className="w-60">
          {/* Account header */}
          <div className="flex items-center gap-3 px-2 py-2">
            {avatar(40)}
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">
                {username}
              </div>
              <div className="truncate text-xs text-muted-foreground">
                {greeting}
              </div>
            </div>
          </div>
          <DropdownMenuSeparator />

          <DropdownMenuItem onSelect={() => navigate("/app/profile")}>
            <User className="h-4 w-4" />
            Profile
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => navigate("/app/settings")}>
            <Settings2 className="h-4 w-4" />
            Settings
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => navigate("/app/settings?tab=tokens")}>
            <KeyRound className="h-4 w-4" />
            API tokens
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => navigate("/app/import")}>
            <Download className="h-4 w-4" />
            Import data
          </DropdownMenuItem>

          <DropdownMenuSeparator />

          {/* Consolidated theme controls — theme / variant / effects. */}
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <Palette className="h-4 w-4" />
              Theme
              <span className="ml-auto text-xs capitalize text-muted-foreground">
                {theme}
              </span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-52">
              <DropdownMenuLabel>Theme</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={theme} onValueChange={setTheme}>
                {THEME_REGISTRY.map((t) => (
                  <DropdownMenuRadioItem key={t.name} value={t.name}>
                    {t.label}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>

              <DropdownMenuSeparator />
              <DropdownMenuLabel>Variant</DropdownMenuLabel>
              <DropdownMenuRadioGroup
                value={variant}
                onValueChange={(v) => setVariant(v as "light" | "dark")}
              >
                <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>

              <DropdownMenuSeparator />
              <DropdownMenuLabel>Effects</DropdownMenuLabel>
              {(
                [
                  ["glow", "Glow"],
                  ["scanlines", "Scanlines"],
                  ["borderAnimations", "Border animations"],
                  ["gradientShift", "Gradient shift"],
                  ["debug", "Debug"],
                ] as const
              ).map(([key, label]) => (
                <DropdownMenuCheckboxItem
                  key={key}
                  checked={effects[key]}
                  // Keep the submenu open while toggling several effects.
                  onSelect={(e) => e.preventDefault()}
                  onCheckedChange={(v) => updateEffect(key, Boolean(v))}
                >
                  {label}
                </DropdownMenuCheckboxItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>

          {/* Quick dark/light flip — the most-used theme action, one click. */}
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault();
              setVariant(isDark ? "light" : "dark");
            }}
          >
            {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            {isDark ? "Light mode" : "Dark mode"}
          </DropdownMenuItem>

          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onLogout}>
            <LogOut className="h-4 w-4" />
            Logout
          </DropdownMenuItem>
        </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
