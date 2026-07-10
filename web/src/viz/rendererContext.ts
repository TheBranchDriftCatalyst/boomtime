import { createContext, useContext } from "react";

/**
 * Renderer context for the strangler-fig graphing system: the whole dashboard
 * can flip between ApexCharts and raw D3 at runtime. Non-component exports live
 * here (separate from RendererProvider.tsx) to keep Fast Refresh happy.
 */

export type Renderer = "apex" | "d3";

export const RENDERER_STORAGE_KEY = "gakatime-renderer";

export const DEFAULT_RENDERER: Renderer = "apex";

export interface RendererContextValue {
  renderer: Renderer;
  setRenderer: (r: Renderer) => void;
  toggle: () => void;
}

export const RendererContext = createContext<RendererContextValue | undefined>(
  undefined,
);

export function readStoredRenderer(): Renderer {
  if (typeof window === "undefined") return DEFAULT_RENDERER;
  try {
    const stored = window.localStorage.getItem(RENDERER_STORAGE_KEY);
    if (stored === "apex" || stored === "d3") return stored;
  } catch {
    // localStorage may throw (private mode) — fall back to the default.
  }
  return DEFAULT_RENDERER;
}

/** Access the active renderer + mutators. Must be used within RendererProvider. */
export function useRenderer(): RendererContextValue {
  const ctx = useContext(RendererContext);
  if (ctx === undefined) {
    throw new Error("useRenderer must be used within a <RendererProvider>");
  }
  return ctx;
}
