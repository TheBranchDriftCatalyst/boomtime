import { createContext, useContext } from "react";

/**
 * Theme context, types, constants, and hook for the boomtime theme system.
 *
 * These non-component exports live in their own module (separate from
 * `ThemeProvider.tsx`) so the provider file only exports a component — which
 * keeps React Fast Refresh happy.
 */

export type Theme = "dark" | "light";

/** localStorage key that holds the persisted preference. */
export const THEME_STORAGE_KEY = "boomtime-theme";

/** Dark is the default theme when nothing is stored. */
export const DEFAULT_THEME: Theme = "dark";

export interface ThemeContextValue {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
}

export const ThemeContext = createContext<ThemeContextValue | undefined>(
  undefined,
);

/**
 * Read the stored preference. Supports the raw values `dark` | `light`, and
 * also understands `system` (resolving it against the OS preference). Any
 * missing/invalid value falls back to the dark default.
 *
 * SSR-safe: returns the default when `window` is unavailable.
 */
export function readStoredTheme(): Theme {
  if (typeof window === "undefined") return DEFAULT_THEME;
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === "dark" || stored === "light") return stored;
    if (stored === "system") {
      return window.matchMedia("(prefers-color-scheme: light)").matches
        ? "light"
        : "dark";
    }
  } catch {
    // localStorage can throw (private mode, disabled cookies) — ignore.
  }
  return DEFAULT_THEME;
}

/**
 * Apply a theme to the document: toggle `.dark` on <html> and keep the native
 * `color-scheme` in sync so form controls / scrollbars match. Guarded for SSR.
 */
export function applyThemeToDocument(theme: Theme): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.classList.toggle("dark", theme === "dark");
  root.style.colorScheme = theme;
}

/**
 * Access the current theme and mutators. Must be used within <ThemeProvider>.
 */
export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (ctx === undefined) {
    throw new Error("useTheme must be used within a <ThemeProvider>");
  }
  return ctx;
}
