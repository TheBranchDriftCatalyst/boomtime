// specs.ts — the FE's read of the canonical widget spec registry (Part B
// Stage 2, gaka-174.x). ONE committed file, internal/widget/specs.json:
// the Go renderer embeds it via //go:embed (internal/widget/spec.go) and
// this module imports the EXACT same bytes through the "@widget-specs"
// alias (vite.config.ts resolve.alias + tsconfig.app.json paths) — no
// codegen, no copy to drift out of sync. This file is a thin typed
// re-export; it does not (yet) drive any FE rendering — see
// internal/widget/spec.go's package doc for the current scope (the Go
// renderSpec engine only, gated behind BOOM_WIDGET_SPEC_ENGINE). A future
// stage can point WidgetRenderer.tsx at this same registry.
import specsRaw from "@widget-specs";

/** Mirrors internal/widget/spec.go's SpecTarget. */
export type SpecTarget = "both" | "fe-only";

/** Mirrors internal/widget/spec.go's panelRect (JSON tags x/y/w/h). */
export interface SpecRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Mirrors internal/widget/spec.go's SpecPanel. */
export interface SpecPanel {
  primitive: string;
  binding: string;
  field?: string;
  rect?: SpecRect;
  title?: string;
}

/** Mirrors internal/widget/spec.go's SpecSize. */
export interface SpecSize {
  w: number;
  h: number;
}

/** Mirrors internal/widget/spec.go's Spec. */
export interface WidgetSpec {
  kind: string;
  target: SpecTarget;
  // Card headline the Go renderSpec engine falls back to when a request
  // omits ?title= (pre-cutover fix, see spec.go's Title doc comment). Not
  // consumed by SpecRenderer.tsx today — the in-page composable dashboard
  // gets its card title from WidgetCatalogEntry.title (catalog.ts) via the
  // surrounding WidgetCard chrome, not from the spec itself.
  title?: string;
  reason?: string;
  size?: SpecSize;
  defaultView?: string;
  panels?: SpecPanel[];
}

export const specs = specsRaw as WidgetSpec[];

/** Lookup a kind's spec. Returns undefined for unknown kinds. */
export function specForKind(kind: string): WidgetSpec | undefined {
  return specs.find((s) => s.kind === kind);
}
