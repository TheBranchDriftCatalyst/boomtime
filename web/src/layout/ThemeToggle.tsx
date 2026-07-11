import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/theme/themeContext";

/**
 * Accessible dark/light toggle button. Drops into the topbar using the same
 * shadcn/Tailwind classes as the existing `outline`/`icon` buttons.
 *
 * Shows a sun while in dark mode (click to go light) and a moon while in light
 * mode (click to go dark) — the icon previews the mode you'll switch to, which
 * is the conventional pattern.
 */
export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme();
  const isDark = theme === "dark";
  const label = isDark ? "Switch to light theme" : "Switch to dark theme";

  return (
    <button
      type="button"
      onClick={toggleTheme}
      title={label}
      aria-label={label}
      className="inline-flex h-9 w-9 items-center justify-center rounded-md border border-input bg-background text-foreground shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
    >
      {isDark ? (
        <Sun className="h-4 w-4" aria-hidden="true" />
      ) : (
        <Moon className="h-4 w-4" aria-hidden="true" />
      )}
    </button>
  );
}
