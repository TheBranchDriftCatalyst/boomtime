// Tiny localStorage persistence wrapper: JSON in/out, guarded so a disabled or
// full localStorage (private mode, quota) degrades to in-memory defaults rather
// than throwing. Keep keys namespaced with a "boomtime-" prefix.

/** Read + JSON-parse a stored value, returning `fallback` on miss/parse error. */
export function loadStored<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    if (raw == null) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

/** JSON-serialize + store a value; silently no-ops if storage is unavailable. */
export function saveStored<T>(key: string, value: T): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // ignore: storage disabled or over quota — persistence is best-effort
  }
}

/** Remove a stored key (best-effort). */
export function clearStored(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {
    // ignore
  }
}
