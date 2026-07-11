// Structured tooltip content — pure functions, no DOM. Every chart in the app
// builds its hover tooltip through this module so structure and styling stay
// consistent (bold title, muted subtitle, label→value rows with optional color
// swatches, muted footer) and every user-controlled string (project / branch /
// file / language name) is HTML-escaped before it lands in innerHTML.
//
// SIBLING BEAD gaka-7m4 (Other-breakdown expansion) consumes `TooltipSpec.rows`
// directly: it feeds a list of member rows (each `{ label: name, value: Hms +
// %, swatch: colorAt(i) }`) with an overflow `footer` like "+3 more". The row
// list already renders with the swatch/muted styling this file emits, so 7m4
// doesn't need to touch layout — only assemble a spec.

/** Escape a string for safe interpolation into innerHTML. */
export function escapeHtml(s: string): string {
  return s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

/**
 * One label→value row in a tooltip. `swatch` is a CSS color that renders as a
 * small colored square before the label (used for member breakdowns and multi-
 * series charts). `muted` renders the whole row at reduced opacity — useful
 * for secondary/less-important context inside a busy tooltip.
 */
export interface TooltipRow {
  label: string;
  value: string;
  swatch?: string;
  muted?: boolean;
}

/**
 * Structured tooltip content. Renders as:
 *   title (bold)
 *   subtitle (muted, e.g. date-range)
 *   rows (label: value grid — with optional swatch)
 *   footer (muted, e.g. "#3 of 14" or "+2 more")
 * Every string in this spec is escaped by `tooltipHtml`. Callers pass raw
 * values — no HTML.
 */
export interface TooltipSpec {
  /** Bold first line — the hovered entity. */
  title: string;
  /** Optional muted second line — temporal or contextual (e.g. "12–18 Jan 2026"). */
  subtitle?: string;
  /** Optional swatch color for the title (multi-series charts). */
  titleSwatch?: string;
  /** Label→value rows rendered as a compact grid. */
  rows?: TooltipRow[];
  /** Optional muted footer — rank, delta, hints. */
  footer?: string;
}

/**
 * Render a `TooltipSpec` as safe HTML. Every user-controlled string is
 * escaped; `footer` may be pre-formatted HTML *from this module's own
 * formatters* (`fmtDelta` produces a colored span) so it is NOT re-escaped —
 * callers must only pass strings they built through `fmt*` helpers or that
 * they escaped themselves.
 */
export function tooltipHtml(spec: TooltipSpec): string {
  const parts: string[] = [];

  const swatch = spec.titleSwatch
    ? `<span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:${escapeHtml(
        spec.titleSwatch,
      )};margin-right:6px;vertical-align:middle"></span>`
    : "";
  parts.push(
    `<div style="font-weight:600">${swatch}${escapeHtml(spec.title)}</div>`,
  );

  if (spec.subtitle) {
    parts.push(
      `<div style="opacity:0.7;font-size:11px;margin-top:1px">${escapeHtml(
        spec.subtitle,
      )}</div>`,
    );
  }

  if (spec.rows && spec.rows.length > 0) {
    parts.push(
      `<div style="margin-top:4px;display:grid;grid-template-columns:auto 1fr;column-gap:10px;row-gap:2px">${spec.rows
        .map((r) => rowHtml(r))
        .join("")}</div>`,
    );
  }

  if (spec.footer) {
    // Footer may embed spans produced by fmtDelta; do not re-escape.
    parts.push(
      `<div style="opacity:0.7;font-size:11px;margin-top:4px">${spec.footer}</div>`,
    );
  }

  return parts.join("");
}

function rowHtml(r: TooltipRow): string {
  const opacity = r.muted ? "opacity:0.7;" : "";
  const swatch = r.swatch
    ? `<span style="display:inline-block;width:8px;height:8px;border-radius:2px;background:${escapeHtml(
        r.swatch,
      )};margin-right:6px;vertical-align:middle"></span>`
    : "";
  return (
    `<div style="${opacity}">${swatch}${escapeHtml(r.label)}</div>` +
    `<div style="${opacity}text-align:right;font-variant-numeric:tabular-nums">${escapeHtml(
      r.value,
    )}</div>`
  );
}

// ---------------------------------------------------------------------------
// Formatters — pure, no DOM. Anything that returns HTML (delta arrows) is
// self-escaped and safe to drop into `footer` verbatim.

/** Format a percentage 0..100 with 1 decimal, clamped to [0, 100]. */
export function fmtPct(x: number): string {
  if (!Number.isFinite(x)) return "0.0%";
  const clamped = Math.max(0, Math.min(100, x));
  return `${clamped.toFixed(1)}%`;
}

/** "#3 of 14". Rank is 1-indexed; returns "" when either arg is non-positive. */
export function fmtRank(rank: number, total: number): string {
  if (!Number.isFinite(rank) || !Number.isFinite(total) || rank < 1 || total < 1)
    return "";
  return `#${Math.round(rank)} of ${Math.round(total)}`;
}

/**
 * Collapsed date range. Same day => "12 Jan 2026"; different =>
 * "12–18 Jan 2026" when in the same month/year, else "28 Dec 2025 – 3 Jan
 * 2026". Inputs are ISO strings; invalid inputs return "".
 *
 * Dates are read as UTC because the app's bucket boundaries and daily series
 * are UTC-aligned; using local time would drift labels by ±1 day depending
 * on the viewer's timezone (browsers *and* CI matter here).
 */
export function fmtDateRange(startISO: string, endISO: string): string {
  const s = new Date(startISO);
  const e = new Date(endISO);
  if (Number.isNaN(s.getTime()) || Number.isNaN(e.getTime())) return "";
  const MONTHS = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun",
    "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
  ];
  const sy = s.getUTCFullYear(), sm = s.getUTCMonth(), sd = s.getUTCDate();
  const ey = e.getUTCFullYear(), em = e.getUTCMonth(), ed = e.getUTCDate();
  const sameDay = sy === ey && sm === em && sd === ed;
  if (sameDay) {
    return `${sd} ${MONTHS[sm]} ${sy}`;
  }
  const sameMonthYear = sy === ey && sm === em;
  if (sameMonthYear) {
    return `${sd}–${ed} ${MONTHS[sm]} ${sy}`;
  }
  const sameYear = sy === ey;
  const startStr = sameYear
    ? `${sd} ${MONTHS[sm]}`
    : `${sd} ${MONTHS[sm]} ${sy}`;
  const endStr = `${ed} ${MONTHS[em]} ${ey}`;
  return `${startStr} – ${endStr}`;
}

/**
 * Format a delta between current and prior period as safe HTML — includes a
 * ▲/▼ arrow with a themed color span. Returns "" when both are 0. When prev
 * is 0 and cur > 0, emits "▲ +Xh (new)" (no % — division would be Infinity).
 * The colored span uses CSS vars `--success` / `--destructive` (fall back to
 * a hex if the theme lacks them — the value is used inline in a style attr).
 */
export function fmtDelta(curSeconds: number, prevSeconds: number): string {
  const cur = Number.isFinite(curSeconds) ? curSeconds : 0;
  const prev = Number.isFinite(prevSeconds) ? prevSeconds : 0;
  if (cur === 0 && prev === 0) return "";
  const diff = cur - prev;
  if (diff === 0) return `<span style="opacity:0.7">no change</span>`;
  const up = diff > 0;
  const arrow = up ? "▲" : "▼";
  const color = up ? "var(--success, #22c55e)" : "var(--destructive, #ef4444)";
  const abs = Math.abs(diff);
  const durStr = shortDuration(abs);
  const sign = up ? "+" : "−";
  let pctStr = "";
  if (prev > 0) {
    const pct = Math.round((diff / prev) * 100);
    pctStr = ` (${up ? "+" : ""}${pct}%)`;
  } else {
    pctStr = " (new)";
  }
  // Escape each interpolated value even though they are numeric-derived; the
  // discipline keeps this helper safe if a caller ever swaps a raw string in.
  return (
    `<span style="color:${color};font-weight:500">${arrow} ${escapeHtml(
      sign + durStr + pctStr,
    )}</span>`
  );
}

/** Compact duration: "1h 12m", "42m", "18s". Never negative. */
function shortDuration(seconds: number): string {
  const n = Math.max(0, Math.round(seconds));
  const h = Math.floor(n / 3600);
  const m = Math.floor((n % 3600) / 60);
  const s = n % 60;
  if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
  if (m > 0) return `${m}m`;
  return `${s}s`;
}
