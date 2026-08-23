import { useCallback, useEffect, useState } from "react";

export interface ChartFrame {
  width: number;
  height: number;
  // Bumped whenever the theme flips so charts re-read CSS custom props.
  themeKey: number;
}

/**
 * Measures a container via ResizeObserver and re-renders on resize.
 * Also watches the document root's class list (which
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
  ref: (node: HTMLDivElement | null) => void;
  node: HTMLDivElement | null;
  frame: ChartFrame;
} {
  const [node, setNode] = useState<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(0);
  const [themeKey, setThemeKey] = useState(0);

  // boom-3nw: a CALLBACK ref (not useRef) so the ResizeObserver attaches
  // whenever the measured node mounts — crucially INCLUDING later than the
  // first render. A chart renders <EmptyChart> (which mounts no ChartSurface,
  // so this ref never attaches) while its query is loading, then swaps in
  // <ChartSurface> once data arrives. The previous `useRef` + `[]`-effect
  // observed exactly once at mount, found `ref.current` still null, and bailed
  // forever — leaving `frame.width` pinned at 0, so useD3Surface's
  // `frame.width === 0` guard skipped the D3 draw and the chart stayed
  // permanently blank. Coding-punchcard was the reliable victim (its query is
  // the slowest, ~2.2s, so it always lost the load race); every EmptyChart-
  // gated chart shared the latent bug. A callback ref fires on every
  // attach/detach, so the observer re-binds when the real node appears.
  const ref = useCallback((n: HTMLDivElement | null) => setNode(n), []);

  useEffect(() => {
    if (!node) return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) setWidth(entry.contentRect.width);
    });
    ro.observe(node);
    setWidth(node.clientWidth);
    return () => ro.disconnect();
  }, [node]);

  useEffect(() => {
    const root = document.documentElement;
    const mo = new MutationObserver(() => setThemeKey((k) => k + 1));
    mo.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => mo.disconnect();
  }, []);

  return { ref, node, frame: { width, height, themeKey } };
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
