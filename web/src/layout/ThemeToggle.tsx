import { Moon, Sun } from "lucide-react";
import { useTheme } from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";

/**
 * Accessible dark/light toggle. Toggles catalyst-ui's `variant` while keeping
 * the boomtime theme selected. Icon previews the mode you'll switch to.
 */
export function ThemeToggle() {
  const { variant, setVariant } = useTheme();
  const isDark = variant === "dark";
  const label = isDark ? "Switch to light theme" : "Switch to dark theme";

  return (
    <button
      type="button"
      onClick={() => setVariant(isDark ? "light" : "dark")}
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
