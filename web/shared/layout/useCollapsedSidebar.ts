import { useCallback, useEffect, useState } from "react";

const SIDEBAR_STORAGE_KEY = "boomtime-sidebar-collapsed";

function readStoredCollapsed(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(SIDEBAR_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

/** The collapsed-sidebar preference, persisted to localStorage across reloads. */
export function useCollapsedSidebar() {
  const [collapsed, setCollapsed] = useState<boolean>(readStoredCollapsed);

  // Persist the collapsed preference so it survives reloads.
  useEffect(() => {
    try {
      window.localStorage.setItem(SIDEBAR_STORAGE_KEY, String(collapsed));
    } catch {
      // ignore storage failures
    }
  }, [collapsed]);

  const toggleCollapsed = useCallback(() => setCollapsed((c) => !c), []);

  return { collapsed, toggleCollapsed };
}
