import { useEffect } from "react";
import { useSyncExternalStore } from "react";
import { useSearchParams } from "react-router";
import { usePublicConfig } from "@/lib/usePublicConfig";

// Beta user-registration preview flag (gaka-93f.1.2).
//
// A global URL param — ?enable_beta_user_registration=true on ANY path —
// forces the new /onboarding flow so we can walk the new registration/
// onboarding UX WHILE STILL LOGGED IN, before it becomes the default. The
// URL is the trigger; sessionStorage is the durable carrier so the flag
// survives navigation within the tab (and clears when the tab closes). This
// mirrors the URL-is-source-of-truth + storage-fallback pattern in
// src/hooks/useTimeRange.ts.
//
// `?enable_beta_user_registration=false` exits the preview. The server can
// also veto the preview instance-wide via /api/v1/config/public
// beta_flags.user_registration=false (an ops kill switch).

const STORAGE_KEY = "boomtime:beta:user_registration";
export const BETA_PARAM = "enable_beta_user_registration";

const listeners = new Set<() => void>();

function readStored(): boolean {
  try {
    return window.sessionStorage.getItem(STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

function emit() {
  for (const l of listeners) l();
}

export function activateBeta() {
  try {
    window.sessionStorage.setItem(STORAGE_KEY, "1");
  } catch {
    /* storage disabled: preview simply won't persist across nav */
  }
  emit();
}

export function exitBeta() {
  try {
    window.sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    /* ignore */
  }
  emit();
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

/** Reactive read of the stored preview flag (independent of URL/server gates). */
export function useBetaStored(): boolean {
  return useSyncExternalStore(subscribe, readStored, () => false);
}

export interface BetaRegistration {
  /** True when the preview is active: stored flag set AND server allows it. */
  active: boolean;
  /** Leave the preview (clears the stored flag). */
  exit: () => void;
}

/**
 * Captures the ?enable_beta_user_registration URL param into the durable
 * store, then reports whether the preview is active (stored AND
 * server-allowed). Safe to call from multiple components — the param capture
 * is idempotent and all consumers share one store.
 */
export function useBetaRegistration(): BetaRegistration {
  const [params] = useSearchParams();
  const stored = useBetaStored();
  const { config } = usePublicConfig();

  // Server kill switch: only false when the server EXPLICITLY disables it.
  // Unknown/loading defaults to allowed so the preview works before config
  // lands.
  // Null-safe: standalone/loading has no server config yet — "unknown defaults
  // to allowed" (a missing config must never crash the app on every path).
  const serverAllows = config?.beta_flags?.user_registration !== false;

  useEffect(() => {
    const v = params.get(BETA_PARAM);
    if (v === "true") activateBeta();
    else if (v === "false") exitBeta();
  }, [params]);

  return { active: stored && serverAllows, exit: exitBeta };
}
