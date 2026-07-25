import type { WidgetScope } from "@/types/api";

// The widget-builder primitive vocabulary. Inert metadata in v1 — its only job
// is to let a future builder UI (v2) enumerate which parts each widget
// composes, so users can eventually assemble graph+badge+label+grade combos.
export type WidgetPrimitive = "graph" | "badge" | "label" | "grade" | "chip";

// gaka-keb: dashboard page-scope markers used by the composable dashboard
// grid. Widgets can appear on multiple dashboard scopes; `profile` means the
// widget is rendered on the public /p/:slug page (in-page React render, NOT
// as an SVG embed). Existing catalog entries carry `profile` alongside the
// SVG scopes when their kind renders on the public dashboard.
export type DashboardScope = "profile" | "overview" | "projects";

// gaka-keb: catalog entries that render in-page under the composable
// dashboard grid can declare per-widget `views` — the chart-toggle pill flips
// between them (e.g. pie ↔ bar). The view name is opaque to the catalog
// (renderer handles it); the layout entry's `view` field carries the user's
// chosen view. When absent, the renderer picks `defaultView`.
export interface WidgetView {
  id: string; // stable slug (e.g. "pie", "bar", "chips")
  label: string; // human label for the toggle pill
  // Lucide icon name (rendered inline in ChartToggle; see the pill component
  // for the resolver). Keeping the icon as a string keeps the catalog
  // JSON-serializable and lets tests introspect the shape.
  icon?: string;
}

export interface WidgetCatalogEntry {
  /** Stable id — MUST match the backend render map (internal/widget/render.go).
   * A Go test (TestKindsMatchFrontendCatalog) guards the two lists against
   * drift; update BOTH when adding a kind. */
  kind: string;
  title: string;
  description: string;
  /** Which page scopes offer this widget in the panel. */
  scopes: WidgetScope[];
  primitives: WidgetPrimitive[];
  /** gaka-keb: dashboard scopes this widget is offered on. Empty/undefined =
   * not offered on any dashboard page (widget is embed-only via /widget/svg).
   * A widget can be `profile`-scope without being SVG-renderable (e.g., chip
   * lists that only make sense in HTML). */
  dashboardScopes?: DashboardScope[];
  /** gaka-keb: default layout footprint for the composable grid (12-col grid,
   * row-height units). Absent = the DashboardGrid falls back to (w=6,h=3). */
  defaultLayout?: { w: number; h: number };
  /** gaka-keb: dual (or triple)-view swap targets for the ChartToggle pill.
   * When present, the widget renders a floating segmented control in its
   * header to switch views. */
  views?: WidgetView[];
  /** gaka-keb: which of `views` to render when the layout entry doesn't pin
   * a view. */
  defaultView?: string;
  /** gaka-keb: SVG-only kinds (renderable via /widget/svg only, NOT via the
   * in-page composable dashboard) can flag themselves to keep the backend
   * SVG endpoint from advertising them. Purely informational for now — the
   * backend keeps its own `kinds` map. Default undefined. */
  svgOnly?: boolean;
}

export const WIDGET_CATALOG: WidgetCatalogEntry[] = [
  {
    kind: "stats-card",
    title: "Stats Card",
    description: "Total time, daily average and top languages",
    scopes: ["user", "project", "space"],
    primitives: ["graph", "label"],
  },
  {
    kind: "stats-card-with-grade",
    title: "Stats Card + Grade",
    // Grade is calibrated per-person (github-readme-stats rank port) — a
    // single project would permanently score C, so user scope only.
    description: "The stats card with a letter grade ring",
    scopes: ["user"],
    primitives: ["graph", "label", "grade"],
  },
  {
    kind: "top-langs",
    title: "Top Languages",
    description: "Your most-used languages as bars",
    scopes: ["user", "project", "space"],
    primitives: ["graph", "label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 4 },
    views: [
      { id: "pie", label: "Pie", icon: "PieChart" },
      { id: "bar", label: "Bar", icon: "BarChart3" },
    ],
    defaultView: "pie",
  },
  {
    kind: "top-projects",
    title: "Top Projects",
    description: "Your most active projects as bars",
    scopes: ["user", "space"],
    primitives: ["graph", "label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 4 },
    views: [
      { id: "pie", label: "Pie", icon: "PieChart" },
      { id: "bar", label: "Bar", icon: "BarChart3" },
    ],
    defaultView: "pie",
  },
  {
    kind: "badge",
    title: "Time Badge",
    description: "A flat shields-style pill with your total time",
    scopes: ["user", "project", "space"],
    primitives: ["badge", "label"],
  },
  // gaka-unq.2 — new twins + composite:
  {
    kind: "activity-heatmap",
    title: "Contribution Calendar",
    description: "Per-day coding activity, GitHub contributions style",
    scopes: ["user", "project", "space"],
    primitives: ["graph"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 12, h: 3 },
  },
  {
    kind: "punchcard",
    title: "Coding Punchcard",
    description: "Hour-of-day × day-of-week intensity grid",
    scopes: ["user", "project", "space"],
    primitives: ["graph"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 4 },
    views: [
      { id: "heatmap", label: "Heatmap", icon: "Grid3x3" },
      { id: "hour-bars", label: "Bars", icon: "BarChart3" },
    ],
    defaultView: "heatmap",
  },
  {
    kind: "momentum",
    title: "Project Momentum",
    description: "Weekly per-project heatmap — who is heating up",
    scopes: ["user", "space"],
    primitives: ["graph", "label"],
  },
  {
    kind: "profile-summary",
    title: "Profile Summary",
    description:
      "Composite 3-panel card: contribution calendar + top languages + grade",
    scopes: ["user"],
    primitives: ["graph", "graph", "grade", "label"],
  },
  // gaka-unq.3 — remaining chart twins:
  {
    kind: "cumulative-area",
    title: "Cumulative Coding Time",
    description: "Filled area of accumulating total time — the growth shape",
    scopes: ["user", "project", "space"],
    primitives: ["graph", "label"],
  },
  {
    kind: "deep-work",
    title: "Deep-Work Sessions",
    description: "Session count + median + longest + daily shape",
    scopes: ["user", "project", "space"],
    primitives: ["label", "graph"],
  },
  {
    kind: "heatmap-projects",
    title: "Activity per Project",
    description: "Day × top-6-projects intensity grid",
    scopes: ["user", "space"],
    primitives: ["graph"],
  },
  {
    kind: "heatmap-languages",
    title: "Activity per Language",
    description: "Day × top-6-languages intensity grid",
    scopes: ["user", "project", "space"],
    primitives: ["graph"],
  },
  // gaka-keb — profile-only kinds. These render only in-page on the
  // composable dashboard grid (no SVG embed variants — they're either
  // interactive-only or trivial enough that a card in SVG would be
  // redundant with existing kinds). `svgOnly: false` is implicit; the
  // backend SVG endpoint returns 404 for kinds it doesn't know.
  {
    kind: "grade-badge",
    title: "Grade Badge",
    description: "Big letter grade poster — port of the github-readme-stats rank",
    scopes: ["user"],
    primitives: ["grade", "label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 3, h: 3 },
  },
  {
    kind: "hero-identity",
    title: "Hero Identity",
    description: "Big username display + tagline + last-updated timestamp",
    scopes: ["user"],
    primitives: ["label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 3 },
  },
  {
    kind: "total-time-stat",
    title: "Total Time",
    description: "Big-numeral stat tile — total tracked time in range",
    scopes: ["user"],
    primitives: ["label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 3, h: 2 },
  },
  {
    kind: "daily-avg-stat",
    title: "Daily Average",
    description: "Big-numeral stat tile — daily average tracked time",
    scopes: ["user"],
    primitives: ["label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 3, h: 2 },
  },
  {
    kind: "current-streak-stat",
    title: "Current Streak",
    description: "Consecutive-days-with-activity ending at today",
    scopes: ["user"],
    primitives: ["label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 3, h: 2 },
  },
  {
    kind: "longest-streak-stat",
    title: "Longest Streak",
    description: "Longest consecutive-days-with-activity in the range",
    scopes: ["user"],
    primitives: ["label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 3, h: 2 },
  },
  {
    kind: "active-days-stat",
    title: "Active Days",
    description: "Ratio of days-with-activity to days-in-range",
    scopes: ["user"],
    primitives: ["label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 3, h: 2 },
  },
  {
    kind: "categories-chart",
    title: "Categories",
    description: "Categorized coding time (coding/debugging/writing)",
    scopes: ["user"],
    primitives: ["graph", "chip"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 4 },
    views: [
      { id: "chips", label: "Chips", icon: "Tag" },
      { id: "pie", label: "Pie", icon: "PieChart" },
    ],
    defaultView: "chips",
  },
  {
    kind: "editors-chips",
    title: "Editors",
    description: "Chip list of editors used, sized by proportion",
    scopes: ["user"],
    primitives: ["chip"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 2 },
  },
  {
    kind: "platforms-chips",
    title: "Platforms",
    description: "Chip list of platforms used, sized by proportion",
    scopes: ["user"],
    primitives: ["chip"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 2 },
  },
  // gaka-wpb — goal tile widgets. All three render in-page only
  // (svgOnly: not set; the backend SVG endpoint doesn't know these
  // kinds and returns 404). Goals are private-by-default; users can
  // still surface progress publicly by adding one of these tiles to
  // their public dashboard layout.
  {
    kind: "goal-progress",
    title: "Goal Progress",
    description: "One goal, horizontal bar with % + name",
    scopes: ["user"],
    primitives: ["graph", "label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 2 },
  },
  {
    kind: "goal-ring",
    title: "Goal Ring",
    description: "Up to 3 goals as concentric progress rings",
    scopes: ["user"],
    primitives: ["grade", "label"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 4, h: 4 },
  },
  {
    kind: "goal-list",
    title: "Goals",
    description: "Mini list of enabled goals with per-row progress",
    scopes: ["user"],
    primitives: ["label", "graph"],
    dashboardScopes: ["profile"],
    defaultLayout: { w: 6, h: 4 },
  },
];

/** Catalog entries offered for a page scope. */
export function catalogFor(scope: WidgetScope): WidgetCatalogEntry[] {
  return WIDGET_CATALOG.filter((e) => e.scopes.includes(scope));
}

/** gaka-keb: catalog entries offered for a composable-dashboard scope
 * (`profile`, `overview`, ...). Distinct from `catalogFor(scope)` because
 * widgets can be embed-scoped without being dashboard-scoped, and vice versa.
 */
export function catalogForDashboard(scope: DashboardScope): WidgetCatalogEntry[] {
  return WIDGET_CATALOG.filter((e) => (e.dashboardScopes ?? []).includes(scope));
}

/** gaka-keb: lookup a catalog entry by kind id. Returns undefined for
 * unknown kinds; the renderer silently drops missing kinds so a stale saved
 * layout doesn't break the page. */
export function widgetByKind(kind: string): WidgetCatalogEntry | undefined {
  return WIDGET_CATALOG.find((e) => e.kind === kind);
}

/** Build the public SVG URL for a widget kind on a minted link. */
export function widgetSvgUrl(
  baseUrl: string,
  kind: string,
  opts: { days: number; theme: string },
): string {
  return `${baseUrl}/${kind}?days=${opts.days}&theme=${opts.theme}`;
}

/** The three copyable embed formats for a widget URL. */
export function embedSnippets(url: string): {
  url: string;
  markdown: string;
  html: string;
} {
  return {
    url,
    markdown: `![Coding stats](${url})`,
    html: `<img src="${url}" alt="Coding stats" />`,
  };
}
