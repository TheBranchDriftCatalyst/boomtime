import { useEffect, useRef, useState } from "react";

export interface ChartFrame {
  width: number;
  height: number;
  // Bumped whenever the theme flips so charts re-read CSS custom props.
  themeKey: number;
}

/**
 * Measures a container via ResizeObserver and re-renders on resize, mirroring
 * ApexCharts' auto-sizing. Also watches the document root's class list (which
 * the ThemeProvider toggles between light/dark) via a MutationObserver and
 * bumps `themeKey` AFTER the class actually changes — so D3 draw effects that
 * depend on it re-run and re-read the (now-updated) CSS custom properties.
 *
 * We deliberately key off the DOM class rather than the React theme value: React
 * runs child effects before parent effects, so a chart reading getComputedStyle
 * on a theme-flip render would otherwise see the stale color before the
 * provider toggled the `.dark` class.
 */
export function useChartFrame(height: number): {
  ref: React.RefObject<HTMLDivElement | null>;
  frame: ChartFrame;
} {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(0);
  const [themeKey, setThemeKey] = useState(0);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) setWidth(entry.contentRect.width);
    });
    ro.observe(el);
    setWidth(el.clientWidth);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    const mo = new MutationObserver(() => setThemeKey((k) => k + 1));
    mo.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => mo.disconnect();
  }, []);

  return { ref, frame: { width, height, themeKey } };
}

/**
 * Resolve a CSS custom property (e.g. "--foreground") to a concrete color
 * string against the document root, so D3 attributes get real values that also
 * work inside SVG. Falls back to the raw token if resolution fails.
 */
export function cssVar(name: string): string {
  if (typeof window === "undefined") return `var(${name})`;
  const v = getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
  return v || `var(${name})`;
}
