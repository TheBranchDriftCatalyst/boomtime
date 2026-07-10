import type { ApexOptions } from "apexcharts";
import { useMemo } from "react";
import { useTheme, type Theme } from "@/theme/themeContext";

/**
 * ApexCharts theming that follows the app's dark/light mode.
 *
 * ApexCharts renders into the DOM, so most colors (axis labels, tooltip,
 * legend) are already driven by the CSS variables in `theme.css` via the
 * `.apexcharts-*` overrides in `index.css`. What Apex does NOT read from CSS is
 * its own `theme.mode` (used for a few internal defaults) and the `foreColor`
 * used for text it draws to canvas. This helper supplies those so charts flip
 * cleanly with the theme.
 */

/**
 * Build the ApexCharts option fragment for a given theme. Merge this LAST into
 * a chart's `options` so it overrides the static `theme`/`foreColor`.
 *
 * Colors reference CSS variables where Apex accepts them (grid, tooltip) so
 * they stay in sync with `theme.css` automatically.
 */
export function apexThemeOptions(theme: Theme): Partial<ApexOptions> {
  const isDark = theme === "dark";
  return {
    theme: { mode: isDark ? "dark" : "light" },
    // Text Apex paints itself (data labels, titles) — use the muted token.
    chart: {
      background: "transparent",
      foreColor: "var(--muted-foreground)",
    },
    grid: {
      borderColor: "var(--border)",
      strokeDashArray: 4,
    },
    tooltip: {
      // `theme` here controls Apex's built-in tooltip palette; the visual
      // styling is further refined by the `.apexcharts-tooltip` CSS rules.
      theme: isDark ? "dark" : "light",
    },
  };
}

/**
 * Shallow-merge helper for ApexCharts options. Merges the theme fragment on top
 * of a chart's base options, combining the nested `chart`, `grid`, and
 * `tooltip` objects one level deep so theme values (foreColor, borderColor,
 * mode) win without discarding the chart's own settings (e.g. `chart.type`).
 *
 * Usage:
 *   const options = mergeApexTheme(baseOptions, useApexTheme());
 */
export function mergeApexTheme(
  base: ApexOptions,
  themeFragment: Partial<ApexOptions>,
): ApexOptions {
  return {
    ...base,
    ...themeFragment,
    chart: { ...base.chart, ...themeFragment.chart },
    grid: { ...base.grid, ...themeFragment.grid },
    tooltip: { ...base.tooltip, ...themeFragment.tooltip },
    theme: { ...base.theme, ...themeFragment.theme },
  };
}

/**
 * Reactive hook: returns the ApexCharts theme fragment for the current mode.
 * Because it reads `useTheme()`, any chart that merges the result re-renders
 * and re-themes when the user flips dark/light.
 */
export function useApexTheme(): Partial<ApexOptions> {
  const { theme } = useTheme();
  return useMemo(() => apexThemeOptions(theme), [theme]);
}
