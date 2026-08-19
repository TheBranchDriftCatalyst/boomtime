// catalogEntries.ts — the flat, presentation-ready list of all 40 widget
// catalog kinds the /catalog gallery iterates over. Derives title/
// description/primitives from WIDGET_CATALOG (web/src/features/widgets/
// catalog.ts — the single source of truth for widget metadata) and `target`
// from internal/widget/specs.json (via specs.ts's specForKind), rather than
// re-declaring either — a kind added to catalog.ts + specs.json picks up its
// title/description/target here automatically; only CATEGORY_BY_KIND needs a
// manual entry (guarded by catalogEntries.test.ts).
import {
  WIDGET_CATALOG,
  widgetSvgUrl,
  embedSnippets,
  type WidgetPrimitive,
} from "@/features/widgets/catalog";
import { specForKind } from "@/features/widgets/specs";
import type { PublicConfig } from "@/types/meta";

/** Boot-config feature flags a catalog entry can be gated behind. A subset of
 * PublicConfig's boolean keys — the flag is read via usePublicConfig() in
 * CatalogPage, and an entry whose flag is off is dropped from the gallery. */
export type CatalogFeatureFlag = "books_enabled";

export interface CatalogWidgetEntry {
  kind: string;
  title: string;
  description: string;
  target: "both" | "fe-only";
  category: string;
  primitives?: WidgetPrimitive[];
  embeddable: boolean;
  /** gaka-qcxg: when set, the gallery only lists this entry when the named
   * PublicConfig flag is on (see CatalogPage's usePublicConfig gate). Undefined
   * = always listed (every pre-existing kind). */
  flag?: CatalogFeatureFlag;
}

/** Ordered, human-friendly groups. "Other" is a deliberate catch-all LAST
 * entry, not a real category any kind should map to today — see
 * CATEGORY_BY_KIND + the guard test in catalogEntries.test.ts, which fails
 * if a WIDGET_CATALOG kind ever falls through to it. */
export const CATALOG_CATEGORIES: string[] = [
  "Identity",
  "Stats",
  "Languages & Projects",
  "Categories",
  "Activity",
  "Streaks",
  "Goals",
  "GitHub",
  "Reading",
  "Overview",
  "Other",
];

const OTHER_CATEGORY = "Other";

// One entry per WIDGET_CATALOG kind. Grouped to read like the catalog's own
// section comments (catalog.ts's gaka-* banners) — see that file for what
// each kind actually renders.
const CATEGORY_BY_KIND: Record<string, string> = {
  // Identity chrome — hero tile, grade poster, awarded-labels showcase,
  // shareable OpenGraph social card.
  "grade-badge": "Identity",
  "hero-identity": "Identity",
  "labels-showcase": "Identity",
  "social-card": "Identity",

  // Headline stat cards + composite.
  "stats-card": "Stats",
  "stats-card-with-grade": "Stats",
  badge: "Stats",
  "total-time-stat": "Stats",
  "daily-avg-stat": "Stats",
  "profile-summary": "Stats",

  // Language / project breakdowns.
  "top-langs": "Languages & Projects",
  "top-projects": "Languages & Projects",
  "heatmap-projects": "Languages & Projects",
  "heatmap-languages": "Languages & Projects",
  "editors-chips": "Languages & Projects",
  "platforms-chips": "Languages & Projects",

  // Category (coding/debugging/building/...) breakdowns.
  "categories-chart": "Categories",
  "category-breakdown": "Categories",
  "category-streamgraph": "Categories",

  // Time-series / heatmap activity.
  "activity-heatmap": "Activity",
  punchcard: "Activity",
  momentum: "Activity",
  "cumulative-area": "Activity",
  loc: "Activity",
  "deep-work": "Activity",
  "overview-total-activity": "Activity",
  "overview-timeline": "Activity",

  // Streaks + consistency.
  "current-streak-stat": "Streaks",
  "longest-streak-stat": "Streaks",
  "active-days-stat": "Streaks",
  "streak-banner": "Streaks",

  // Goals.
  "goal-progress": "Goals",
  "goal-ring": "Goals",
  "goal-list": "Goals",

  // GitHub.
  "github-commits": "GitHub",
  "github-repos": "GitHub",
  "github-languages": "GitHub",
  "github-stats": "GitHub",

  // Reading domain (gaka-qcxg) — books_enabled-gated fe-only tiles.
  "reading-listening-trend": "Reading",
  "reading-books-by-genre": "Reading",
  "reading-top-series": "Reading",
  "reading-finished-per-month": "Reading",
  "reading-listening-in-range": "Reading",

  // Overview-only ambient overlays.
  "overview-stats": "Overview",
  "ai-assistance": "Overview",
  wellness: "Overview",
};

// gaka-qcxg: kinds the gallery only lists when a boot-config feature flag is
// on. Guarded by catalogEntries.test.ts (the reading kinds must map to
// books_enabled) so a new domain-gated kind can't silently ship ungated.
const FLAG_BY_KIND: Record<string, CatalogFeatureFlag> = {
  "reading-listening-trend": "books_enabled",
  "reading-books-by-genre": "books_enabled",
  "reading-top-series": "books_enabled",
  "reading-finished-per-month": "books_enabled",
  "reading-listening-in-range": "books_enabled",
};

export const CATALOG_WIDGETS: CatalogWidgetEntry[] = WIDGET_CATALOG.map((entry) => {
  const spec = specForKind(entry.kind);
  const target: "both" | "fe-only" = spec?.target === "both" ? "both" : "fe-only";
  return {
    kind: entry.kind,
    title: entry.title,
    description: entry.description,
    target,
    category: CATEGORY_BY_KIND[entry.kind] ?? OTHER_CATEGORY,
    primitives: entry.primitives,
    embeddable: target === "both",
    flag: FLAG_BY_KIND[entry.kind],
  };
});

/** The gallery's visible slice for a given boot config — drops any entry whose
 * `flag` is off. CatalogPage feeds this from usePublicConfig(); a widget with
 * no flag is always kept. */
export function visibleCatalogWidgets(config: PublicConfig): CatalogWidgetEntry[] {
  return CATALOG_WIDGETS.filter((w) => !w.flag || config[w.flag]);
}

// Re-exported so the parent's card component can offer copy-embed actions
// for `embeddable` kinds without a second import path into catalog.ts.
export { widgetSvgUrl, embedSnippets };
