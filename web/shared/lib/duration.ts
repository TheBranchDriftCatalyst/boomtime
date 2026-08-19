// duration.ts — human-friendly duration ↔ seconds parser/formatter.
//
// Accepts short-form durations like "1h30m", "2d", "7d", "45m", "3600s"
// and returns seconds. Bare numbers (no unit) are treated as seconds
// so existing "3600" inputs stay valid.
//
// Units:
//   s → 1
//   m → 60
//   h → 3600
//   d → 86400
//   w → 604800
//
// Deliberately NO "month" or "year" — those are calendar-dependent and
// the goal engine works on rolling second windows. Users spelling out
// "1M" or "1y" get a null (invalid) rather than a silently-wrong scale.
//
// Composability: units may appear in any order and any count, so long
// as each has a numeric prefix. "1h30m" == "30m1h" == 5400. Whitespace
// is tolerated between components ("1h 30m").

const UNIT_SECONDS: Record<string, number> = {
  s: 1,
  m: 60,
  h: 3600,
  d: 86400,
  w: 604800,
};

const TOKEN_RE = /(\d+)([smhdw])/gi;

/**
 * Parse a duration string into seconds. Returns null when the input is
 * empty, malformed, or contains an unknown unit. Bare integers (no
 * unit) are interpreted as seconds — legacy compatibility with the
 * pre-shortcut input.
 */
export function parseDuration(input: string): number | null {
  if (input == null) return null;
  const trimmed = String(input).trim();
  if (trimmed === "") return null;

  // Bare integer (no letters) → seconds.
  if (/^\d+$/.test(trimmed)) {
    return Number(trimmed);
  }

  // Reject anything that has letters we don't recognize. First strip valid
  // <digits><unit> tokens + whitespace, then require the residue to be empty.
  const residue = trimmed.toLowerCase().replace(TOKEN_RE, "").replace(/\s+/g, "");
  if (residue !== "") return null;

  // Sum every matched token. TOKEN_RE has `g`, so use matchAll.
  let total = 0;
  let matched = false;
  for (const m of trimmed.toLowerCase().matchAll(TOKEN_RE)) {
    matched = true;
    const n = Number(m[1]);
    const unit = m[2] as keyof typeof UNIT_SECONDS;
    total += n * UNIT_SECONDS[unit];
  }
  return matched ? total : null;
}

/**
 * Format seconds back into the canonical short-form string. Chooses
 * the LARGEST unit that divides evenly, then continues with smaller
 * units for any remainder. Zero → "0s".
 *
 *   formatDuration(3600)  → "1h"
 *   formatDuration(5400)  → "1h30m"
 *   formatDuration(604800) → "1w"
 *   formatDuration(0)     → "0s"
 *   formatDuration(1)     → "1s"
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "";
  if (seconds === 0) return "0s";
  const units: Array<[string, number]> = [
    ["w", 604800],
    ["d", 86400],
    ["h", 3600],
    ["m", 60],
    ["s", 1],
  ];
  let remaining = Math.floor(seconds);
  const parts: string[] = [];
  for (const [label, size] of units) {
    if (remaining >= size) {
      const n = Math.floor(remaining / size);
      parts.push(`${n}${label}`);
      remaining -= n * size;
    }
  }
  return parts.join("");
}
