import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Format a number of seconds like the original dashboard, e.g. "14 hrs 32 min".
 * Ported from hakatime's utils.secondsToHms.
 */
export function secondsToHms(input: number | null | undefined): string {
  const d = Number(input ?? 0);
  const h = Math.floor(d / 3600);
  const m = Math.floor((d % 3600) / 60);
  const s = d < 60 ? Math.floor(d) : 0;

  const hDisplay = h > 0 ? `${h}${h === 1 ? " hr " : " hrs "}` : "";
  const mDisplay = m > 0 ? `${m}${m === 1 ? " min " : " mins "}` : "";
  const sDisplay = s > 0 ? `${s}${s === 1 ? " sec " : " secs "}` : "";

  const out = (hDisplay + mDisplay + sDisplay).trim();
  return out || "0 mins";
}

export function truncate(input: string, num: number): string {
  return input.length > num ? `${input.substring(0, num)}...` : input;
}

/** ISO date (yyyy-mm-dd) */
export function formatDate(d: string | number | Date): string {
  return new Date(d).toISOString().slice(0, 10);
}

/** Compact elapsed duration between two instants, e.g. "1h 04m 09s". */
export function formatElapsed(
  from: string | number | Date | null,
  to: string | number | Date | null,
): string {
  if (!from) return "-";
  const startMs = new Date(from).getTime();
  const endMs = to ? new Date(to).getTime() : Date.now();
  let secs = Math.max(0, Math.floor((endMs - startMs) / 1000));
  const h = Math.floor(secs / 3600);
  secs -= h * 3600;
  const m = Math.floor(secs / 60);
  const s = secs - m * 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  if (h > 0) return `${h}h ${pad(m)}m ${pad(s)}s`;
  if (m > 0) return `${m}m ${pad(s)}s`;
  return `${s}s`;
}

/** Human-readable byte size, e.g. 2048 -> "2.0 KB". */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const u = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${u[i]}`;
}

export function removeDays(d: Date, num: number): Date {
  const d1 = new Date(d);
  d1.setDate(d.getDate() - num);
  return d1;
}

export function removeHours(d: Date, num: number): Date {
  const d1 = new Date(d);
  d1.setHours(d.getHours() - num);
  return d1;
}

/** Inclusive list of dates between start and end (as ISO date strings). */
export function daysBetween(start: Date, end: Date): string[] {
  const arr: string[] = [];
  const dt = new Date(start);
  while (dt <= end) {
    arr.push(new Date(dt).toISOString());
    dt.setDate(dt.getDate() + 1);
  }
  return arr;
}

export async function copyToClipboard(v: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(v);
  } catch {
    // Fallback for insecure contexts.
    const elem = document.createElement("textarea");
    elem.value = v;
    elem.setAttribute("readonly", "");
    elem.style.position = "absolute";
    elem.style.left = "-99999px";
    document.body.appendChild(elem);
    elem.select();
    document.execCommand("copy");
    document.body.removeChild(elem);
  }
}

/**
 * Shift an hour-of-day value by the local timezone offset, mirroring the
 * original dashboard's addTimeOffset. Returns an index 0-23.
 */
export function addTimeOffset(v: string | number): number {
  const n = parseInt(String(v), 10);
  const offset = new Date().getTimezoneOffset() / 60;
  return ((((n - offset) % 24) + 24) % 24) | 0;
}
