import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  applyThemeToDocument,
  readStoredTheme,
  ThemeContext,
  THEME_STORAGE_KEY,
  type Theme,
  type ThemeContextValue,
} from "@/theme/themeContext";

// Re-export types only (erased at build time, so they don't affect Fast
// Refresh). Runtime values like `useTheme` live in `@/theme/themeContext` and
// must be imported from there directly.
export type { Theme, ThemeContextValue } from "@/theme/themeContext";

/**
 * Theme system for boomtime.
 *
 * Dark is the DEFAULT: on first visit (nothing stored) the app renders dark.
 * The chosen theme persists to localStorage under `boomtime-theme` and is
 * applied by toggling the `.dark` class (and `color-scheme`) on <html>.
 */
export function ThemeProvider({ children }: { children: ReactNode }) {
  // Lazy initializer runs once and reads localStorage synchronously so the
  // first client render already reflects the stored/default choice.
  const [theme, setThemeState] = useState<Theme>(() => readStoredTheme());

  // Keep <html> in sync whenever the theme changes (also runs on mount so the
  // class is applied even if the inline pre-paint script is absent).
  useEffect(() => {
    applyThemeToDocument(theme);
  }, [theme]);

  const setTheme = useCallback((next: Theme) => {
    setThemeState(next);
    if (typeof window !== "undefined") {
      try {
        window.localStorage.setItem(THEME_STORAGE_KEY, next);
      } catch {
        // Persisting is best-effort; ignore storage failures.
      }
    }
  }, []);

  const toggleTheme = useCallback(() => {
    setThemeState((prev) => {
      const next: Theme = prev === "dark" ? "light" : "dark";
      if (typeof window !== "undefined") {
        try {
          window.localStorage.setItem(THEME_STORAGE_KEY, next);
        } catch {
          // ignore
        }
      }
      return next;
    });
  }, []);

  const value = useMemo<ThemeContextValue>(
    () => ({ theme, setTheme, toggleTheme }),
    [theme, setTheme, toggleTheme],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
}
