// DashboardEditSidebar (boom-lzr, Phase 4) — the edit rail. Two-state by
// design (mirrors the profile ProfileEditor palette):
//
//   - Nothing selected → CATALOG: the widgets NOT already in the layout, each a
//     button that adds it at the bottom of the grid.
//   - A tile selected  → CONFIG: the per-widget config form. Phase 4 leaves a
//     TODO placeholder; the real form is Phase 5 (boom-lzr config schema).
//
// The palette entries (catalog − in-layout) are computed by the caller
// (DashboardEditor) so this component stays presentational.
import { Plus } from "lucide-react";
import type { WidgetCatalogEntry } from "@shared/features/widgets/catalog";

export interface DashboardEditSidebarProps {
  /** Catalog entries NOT currently placed in the layout (the "add" palette). */
  paletteEntries: WidgetCatalogEntry[];
  /** The selected tile's key, or null when nothing is selected. */
  selectedKey: string | null;
  /** Add a widget kind to the layout. */
  onAdd: (kind: string) => void;
}

export function DashboardEditSidebar({
  paletteEntries,
  selectedKey,
  onAdd,
}: DashboardEditSidebarProps) {
  // CONFIG state — a tile is selected. Phase 5 fills this in with the per-kind
  // config form (title override / chart-view toggle / top-N).
  if (selectedKey) {
    return (
      <div className="flex flex-col gap-2" data-testid="dashboard-edit-config">
        <div className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground">
          &gt; CONFIGURE
        </div>
        <div className="rounded border border-border/60 p-3 text-xs">
          <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.1em] text-primary">
            {selectedKey}
          </div>
          {/* TODO(boom-lzr Phase 5): render the per-widget config form
              (WidgetConfigForm) — title override, chart-view toggle, top-N. */}
          <p className="mt-2 text-[10px] text-muted-foreground">
            Widget configuration is coming soon. For now, drag to move, drag a
            corner to resize, or remove with the × on the tile.
          </p>
        </div>
      </div>
    );
  }

  // CATALOG state — nothing selected. List addable widgets.
  return (
    <div className="flex flex-col gap-2" data-testid="dashboard-edit-catalog">
      <div className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground">
        &gt; ADD WIDGET ({paletteEntries.length})
      </div>
      {paletteEntries.length === 0 && (
        <div className="text-xs text-muted-foreground">
          All widgets are in your layout.
        </div>
      )}
      {paletteEntries.map((e) => (
        <button
          key={e.kind}
          type="button"
          onClick={() => onAdd(e.kind)}
          className="flex items-start justify-between gap-2 rounded border border-border/60 p-2 text-left text-xs hover:border-primary/60"
          data-testid={`dashboard-edit-add-${e.kind}`}
        >
          <div>
            <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.1em] text-primary">
              {e.title}
            </div>
            <div className="mt-1 text-[10px] text-muted-foreground">
              {e.description}
            </div>
          </div>
          <Plus size={14} className="shrink-0" />
        </button>
      ))}
    </div>
  );
}
