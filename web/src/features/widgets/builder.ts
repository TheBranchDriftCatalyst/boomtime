// Builder types + spec encoding (gaka-567). Mirrors internal/widget/custom.go:
// keep the WidgetPanelKind / WidgetLayout / WidgetDef shapes in sync — the
// backend Def decoder validates against the same whitelist.

export type WidgetLayout =
  | "1-panel"
  | "2-panel-h"
  | "2-panel-v"
  | "3-panel-h";

export type WidgetPanelKind =
  | "calendar"
  | "top-langs"
  | "top-projects"
  | "grade"
  | "area"
  | "punchcard"
  | "momentum"
  | "metrics";

export interface WidgetDef {
  layout: WidgetLayout;
  title?: string;
  panels: { kind: WidgetPanelKind; title?: string }[];
}

// Panel catalog for the builder UI — label + short description + which
// primitive category it belongs to (matches WidgetCatalogEntry.primitives so
// the composed spec can also enumerate its primitives).
export const PANEL_CATALOG: {
  kind: WidgetPanelKind;
  label: string;
  hint: string;
}[] = [
  { kind: "calendar", label: "Calendar", hint: "GitHub-style contribution grid" },
  { kind: "area", label: "Area line", hint: "Cumulative coding time" },
  { kind: "top-langs", label: "Top languages", hint: "Bar list, top-N by seconds" },
  { kind: "top-projects", label: "Top projects", hint: "Bar list, top-N by seconds" },
  { kind: "grade", label: "Grade ring", hint: "S/A+/A/… letter + total" },
  { kind: "punchcard", label: "Punchcard", hint: "Day × hour intensity grid" },
  { kind: "momentum", label: "Momentum grid", hint: "Weeks × top projects" },
  { kind: "metrics", label: "Metric stack", hint: "Total + daily avg (+ sessions)" },
];

export const LAYOUT_CATALOG: { layout: WidgetLayout; label: string; panels: number }[] = [
  { layout: "1-panel", label: "Single panel", panels: 1 },
  { layout: "2-panel-h", label: "Two horizontal", panels: 2 },
  { layout: "2-panel-v", label: "Two stacked", panels: 2 },
  { layout: "3-panel-h", label: "Three horizontal", panels: 3 },
];

export function panelCount(layout: WidgetLayout): number {
  return LAYOUT_CATALOG.find((l) => l.layout === layout)?.panels ?? 1;
}

// encodeDef → url-safe base64 of JSON. Mirrors internal/widget's
// base64.RawURLEncoding — no padding, - and _ substituted.
export function encodeDef(def: WidgetDef): string {
  const json = JSON.stringify(def);
  const b64 = btoa(unescape(encodeURIComponent(json)));
  return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** Build the public custom-widget URL for a minted link's base + a spec. */
export function customWidgetUrl(
  baseUrl: string,
  def: WidgetDef,
  opts: { days: number; theme: string },
): string {
  const spec = encodeDef(def);
  return `${baseUrl}/custom?spec=${spec}&days=${opts.days}&theme=${opts.theme}`;
}
