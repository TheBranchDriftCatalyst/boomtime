import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import {
  RendererContext,
  RENDERER_STORAGE_KEY,
  readStoredRenderer,
  type Renderer,
} from "@/viz/rendererContext";

export function RendererProvider({ children }: { children: ReactNode }) {
  const [renderer, setRendererState] = useState<Renderer>(readStoredRenderer);

  // Persist the choice so it survives reloads.
  useEffect(() => {
    try {
      window.localStorage.setItem(RENDERER_STORAGE_KEY, renderer);
    } catch {
      // ignore storage failures
    }
  }, [renderer]);

  const setRenderer = useCallback((r: Renderer) => setRendererState(r), []);
  const toggle = useCallback(
    () => setRendererState((r) => (r === "apex" ? "d3" : "apex")),
    [],
  );

  const value = useMemo(
    () => ({ renderer, setRenderer, toggle }),
    [renderer, setRenderer, toggle],
  );

  return (
    <RendererContext.Provider value={value}>
      {children}
    </RendererContext.Provider>
  );
}
