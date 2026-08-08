// defaults.ts — default layouts per dashboard scope (gaka-keb). Kept
// separate from the grid primitive so the primitive stays boomtime-
// agnostic.
//
// The default layout for `public_profile` is the design brief's asymmetric
// magazine layout:
//   Row 1 (h=3): [grade-badge 3] [hero-identity 6] [current-streak 3]
//   Row 2 (h=2): [total-time 3] [daily-avg 3] [longest-streak 3] [active-days 3]
//   Row 3 (h=3): [activity-heatmap 12]
//   Row 4 (h=4): [top-langs 6] [punchcard 6]
//   Row 5 (h=4): [top-projects 6] [categories-chart 6]
//   Row 6 (h=2): [editors-chips 6] [platforms-chips 6]
import type { GridLayoutItem } from "@/lib/grid";

export const PUBLIC_PROFILE_DEFAULT_LAYOUT: GridLayoutItem[] = [
  // Row 1
  { i: "grade-badge",         x: 0,  y: 0,  w: 3,  h: 3, view: null },
  { i: "hero-identity",       x: 3,  y: 0,  w: 6,  h: 3, view: null },
  { i: "current-streak-stat", x: 9,  y: 0,  w: 3,  h: 3, view: null },
  // Row 2 (metric strip)
  { i: "total-time-stat",     x: 0,  y: 3,  w: 3,  h: 2, view: null },
  { i: "daily-avg-stat",      x: 3,  y: 3,  w: 3,  h: 2, view: null },
  { i: "longest-streak-stat", x: 6,  y: 3,  w: 3,  h: 2, view: null },
  { i: "active-days-stat",    x: 9,  y: 3,  w: 3,  h: 2, view: null },
  // Row 3 (full-bleed calendar)
  { i: "activity-heatmap",    x: 0,  y: 5,  w: 12, h: 3, view: null },
  // Row 4
  { i: "top-langs",           x: 0,  y: 8,  w: 6,  h: 4, view: "pie" },
  { i: "punchcard",           x: 6,  y: 8,  w: 6,  h: 4, view: "heatmap" },
  // Row 5
  { i: "top-projects",        x: 0,  y: 12, w: 6,  h: 4, view: "pie" },
  { i: "categories-chart",    x: 6,  y: 12, w: 6,  h: 4, view: "chips" },
  // Row 6
  { i: "editors-chips",       x: 0,  y: 16, w: 6,  h: 2, view: null },
  { i: "platforms-chips",     x: 6,  y: 16, w: 6,  h: 2, view: null },
  // Row 7 (gaka-2ud P5): combined GitHub stats card. Self-hides when the owner
  // hasn't connected GitHub / has no public GitHub data, so it's safe to ship
  // in the default layout for everyone — a GitHub-less profile just doesn't
  // render this tile (no gap, no CTA).
  { i: "github-stats",        x: 0,  y: 18, w: 12, h: 8, view: null },
];

/** Count of widgets in the shipped default. Handy for tests + reports. */
export const PUBLIC_PROFILE_DEFAULT_WIDGET_COUNT =
  PUBLIC_PROFILE_DEFAULT_LAYOUT.length;
