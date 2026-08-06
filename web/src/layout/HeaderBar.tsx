import { useNavigate } from "react-router";
import {
  Download,
  KeyRound,
  LogOut,
  Moon,
  Palette,
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

interface HeaderBarProps {
  username: string;
  onLogout: () => void;
}

function greetingFor(hour: number): string {
  if (hour < 5) return "Burning the midnight oil";
  if (hour < 12) return "Good morning";
  if (hour < 18) return "Good afternoon";
  return "Good evening";
}

/**
 * Top header — a single consolidated user menu (gaka-lzr). The previous bar had
 * TWO overlapping theme controls (a full ThemeSwitcher + a redundant sun/moon
 * ThemeToggle) plus a bare avatar. Everything now lives in one avatar dropdown:
 * account nav, a Theme submenu (theme / variant / effects), a quick dark-mode
 * toggle, and logout. The left side carries a light quick-look greeting.
 */
export function HeaderBar({ username, onLogout }: HeaderBarProps) {
  const navigate = useNavigate();
  const { theme, setTheme, variant, setVariant, effects, updateEffect } =
    useTheme();
  const isDark = variant === "dark";
  const greeting = greetingFor(new Date().getHours());

  const avatar = (size: number) => (
    <span
      className="inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full ring-1 ring-border"
      style={{ width: size, height: size }}
    >
      <UserAvatarImage
        username={username}
        size={size}
        alt={`${username}'s avatar`}
      />
    </span>
  );

  return (
    <header className="flex h-16 items-center justify-between gap-3 border-b bg-card px-6">
      {/* Quick-look: a time-aware greeting. */}
      <div className="hidden items-baseline gap-1.5 text-sm md:flex">
        <span className="text-muted-foreground">{greeting},</span>
        <span className="font-semibold text-foreground">{username}</span>
      </div>

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
    </header>
  );
}
