import { useCallback, useEffect, useState } from "react";

// Storage key for the first-run welcome modal. Set to "1" once the user
// dismisses/completes the modal; a fresh browser or incognito window has an
// absent key and sees the modal again.
export const WELCOMED_KEY = "boomtime-welcomed";

function readWelcomed(): boolean {
  try {
    return window.localStorage.getItem(WELCOMED_KEY) === "1";
  } catch {
    // SSR / disabled storage: treat as welcomed so the modal never blocks.
    return true;
  }
}

function writeWelcomed() {
  try {
    window.localStorage.setItem(WELCOMED_KEY, "1");
  } catch {
    /* ignore */
  }
}

export interface FirstRunWelcome {
  open: boolean;
  dismiss: () => void;
}

/**
 * Gates the first-run welcome modal on a localStorage flag. The modal opens
 * exactly once per browser: any subsequent visit finds the flag set and stays
 * closed. Read on mount so SSR-safe callers don't hydrate open.
 */
export function useFirstRunWelcome(): FirstRunWelcome {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!readWelcomed()) setOpen(true);
  }, []);

  const dismiss = useCallback(() => {
    writeWelcomed();
    setOpen(false);
  }, []);

  return { open, dismiss };
}
