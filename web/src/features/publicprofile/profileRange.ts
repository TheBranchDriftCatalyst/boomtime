// profileRange.ts — the selected stats window for /p/:slug (gaka-174.7).
//
// Module-level store (same pattern as featureFlags) so the range control in
// the floating dossier cluster and the data-fetchers (PublicDashboard /
// ProfileEditor) share one value without prop-drilling or a provider.
// Transient by design — resets to the canonical 60d default on reload; a
// re-scoped view is a momentary "how am I looking at this", not a saved pref.
import { useCallback, useSyncExternalStore } from "react";

export interface RangePreset {
  days: number;
  label: string;
}

// null days on the API = server default (canonical 60d). We keep 60 explicit
// here so the control has a highlighted "60D" option.
export const RANGE_PRESETS: RangePreset[] = [
  { days: 7, label: "7D" },
  { days: 30, label: "30D" },
  { days: 60, label: "60D" },
  { days: 90, label: "90D" },
  { days: 365, label: "1Y" },
];

export const DEFAULT_RANGE_DAYS = 60;

let current = DEFAULT_RANGE_DAYS;
const listeners = new Set<() => void>();

export function getRangeDays(): number {
  return current;
}
export function setRangeDays(days: number): void {
  current = days;
  listeners.forEach((l) => l());
}

export function useProfileRange(): readonly [number, (days: number) => void] {
  const subscribe = useCallback((cb: () => void) => {
    listeners.add(cb);
    return () => listeners.delete(cb);
  }, []);
  const days = useSyncExternalStore(
    subscribe,
    () => current,
    () => DEFAULT_RANGE_DAYS,
  );
  const set = useCallback((d: number) => setRangeDays(d), []);
  return [days, set] as const;
}
