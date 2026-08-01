// featureFlags.ts — tiny client-side feature-flip system (gaka-174).
//
// localStorage-backed, per-browser, default-off unless a flag opts in. A flag
// is a viewer preference ("show me the experimental thing"), not server state.
// useFeatureFlag() subscribes via useSyncExternalStore so every consumer +
// the flipper UI stay in sync when a flag is toggled in the same tab.
import { useCallback, useSyncExternalStore } from "react";

export interface FeatureFlagDef {
  key: string;
  label: string;
  description?: string;
  /** Value when the viewer has never set it. */
  default: boolean;
}

// The registry. Add a flag = one entry here.
export const FEATURE_FLAGS: FeatureFlagDef[] = [
  {
    key: "labels3D",
    label: "3D award medallions",
    description:
      "Render the labels showcase as glossy 3D coins instead of the classic chips.",
    default: false,
  },
];

const PREFIX = "boomtime:flag:";
const listeners = new Set<() => void>();

function defOf(key: string): boolean {
  return FEATURE_FLAGS.find((f) => f.key === key)?.default ?? false;
}

export function getFlag(key: string): boolean {
  try {
    const raw = window.localStorage.getItem(PREFIX + key);
    return raw === null ? defOf(key) : raw === "1";
  } catch {
    return defOf(key);
  }
}

export function setFlag(key: string, on: boolean): void {
  try {
    window.localStorage.setItem(PREFIX + key, on ? "1" : "0");
  } catch {
    /* storage disabled — flag stays at default */
  }
  listeners.forEach((l) => l());
}

/** Reactive read + setter for a single flag. */
export function useFeatureFlag(key: string): readonly [boolean, (on: boolean) => void] {
  const subscribe = useCallback((cb: () => void) => {
    listeners.add(cb);
    return () => listeners.delete(cb);
  }, []);
  const value = useSyncExternalStore(
    subscribe,
    () => getFlag(key),
    () => defOf(key),
  );
  const set = useCallback((on: boolean) => setFlag(key, on), [key]);
  return [value, set] as const;
}
