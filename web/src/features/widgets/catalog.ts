import type { WidgetScope } from "@/types/api";

// The widget-builder primitive vocabulary. Inert metadata in v1 — its only job
// is to let a future builder UI (v2) enumerate which parts each widget
// composes, so users can eventually assemble graph+badge+label+grade combos.
export type WidgetPrimitive = "graph" | "badge" | "label" | "grade";

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
  },
  {
    kind: "top-projects",
    title: "Top Projects",
    description: "Your most active projects as bars",
    scopes: ["user", "space"],
    primitives: ["graph", "label"],
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
  },
  {
    kind: "punchcard",
    title: "Coding Punchcard",
    description: "Hour-of-day × day-of-week intensity grid",
    scopes: ["user", "project", "space"],
    primitives: ["graph"],
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
];

/** Catalog entries offered for a page scope. */
export function catalogFor(scope: WidgetScope): WidgetCatalogEntry[] {
  return WIDGET_CATALOG.filter((e) => e.scopes.includes(scope));
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
