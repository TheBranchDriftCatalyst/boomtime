// DashboardEditSidebar (boom-lzr, Phase 4 + Phase 5) — the edit rail.
// Two-state by design (mirrors the profile ProfileEditor palette):
//
//   - Nothing selected → CATALOG: the widgets NOT already in the layout, each a
//     button that adds it at the bottom of the grid.
//   - A tile selected  → CONFIGURE: a GENERIC per-widget config form driven by
//     the catalog entry (Phase 5) — a view selector (when the kind has
//     `views`), a title override, an optional per-tile range override, and a
//     hidden/visibility toggle. Deliberately NOT a bespoke schema per widget
//     kind (that's the larger boom-5ox) — every kind gets the same four
//     fields, some of which no-op when not applicable (e.g. no `views`).
//
// The palette entries (catalog − in-layout) are computed by the caller
// (DashboardEditor) so this component stays presentational.
import { useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/select";
import type { GridLayoutItem } from "@shared/lib/grid";
import type { WidgetCatalogEntry } from "@shared/features/widgets/catalog";

// Range-override presets (days). "inherit" (undefined config.rangeDays) means
// "use the dashboard's own date-range picker" — the default for every tile.
const RANGE_PRESETS: { value: string; label: string; days: number | undefined }[] = [
  { value: "inherit", label: "Dashboard range", days: undefined },
  { value: "7", label: "Last 7 days", days: 7 },
  { value: "14", label: "Last 14 days", days: 14 },
  { value: "30", label: "Last 30 days", days: 30 },
  { value: "90", label: "Last 90 days", days: 90 },
];

export interface DashboardEditSidebarProps {
  /** Catalog entries NOT currently placed in the layout (the "add" palette). */
  paletteEntries: WidgetCatalogEntry[];
  /** The selected tile's key, or null when nothing is selected. */
  selectedKey: string | null;
  /** Add a widget kind to the layout. */
  onAdd: (kind: string) => void;
  /** The selected tile's current layout entry, or null when nothing is
   * selected (or the selected key no longer exists in the layout). */
  selectedItem: GridLayoutItem | null;
  /** The catalog entry backing the selected tile. Null for a stale/unknown
   * kind (a saved layout can reference a kind removed from the catalog) —
   * the form degrades to "remove this tile" guidance in that case. */
  selectedEntry: WidgetCatalogEntry | null;
  /** Persist the tile's chart-toggle view (only relevant when the catalog
   * entry declares `views`). */
  onSetView: (key: string, view: string) => void;
  /** Replace the tile's opaque config blob wholesale — callers pass the full
   * merged object (`{ ...current, title: "..." }`), same shape as the
   * store's setConfig action. */
  onSetConfig: (key: string, config: Record<string, unknown> | undefined) => void;
  /** Toggle the tile's "hide but keep placement" flag. */
  onSetHidden: (key: string, hidden: boolean) => void;
}

export function DashboardEditSidebar({
  paletteEntries,
  selectedKey,
  onAdd,
  selectedItem,
  selectedEntry,
  onSetView,
  onSetConfig,
  onSetHidden,
}: DashboardEditSidebarProps) {
  // CONFIGURE state — a tile is selected.
  if (selectedKey) {
    if (!selectedItem) {
      // Selection references a key no longer in the layout (e.g. removed by
      // undo racing a click) — nothing to configure.
      return (
        <div className="flex flex-col gap-2" data-testid="dashboard-edit-config">
          <div className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground">
            &gt; CONFIGURE
          </div>
          <p className="text-xs text-muted-foreground">
            This tile is no longer in the layout.
          </p>
        </div>
      );
    }
    return (
      <TileConfigForm
        key={selectedItem.i}
        item={selectedItem}
        entry={selectedEntry}
        onSetView={onSetView}
        onSetConfig={onSetConfig}
        onSetHidden={onSetHidden}
      />
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

interface TileConfigFormProps {
  item: GridLayoutItem;
  entry: WidgetCatalogEntry | null;
  onSetView: (key: string, view: string) => void;
  onSetConfig: (key: string, config: Record<string, unknown> | undefined) => void;
  onSetHidden: (key: string, hidden: boolean) => void;
}

/** The generic view/title/range/visibility form (boom-lzr Phase 5). One
 * component for every widget kind — NOT a bespoke schema per kind. */
function TileConfigForm({ item, entry, onSetView, onSetConfig, onSetHidden }: TileConfigFormProps) {
  const config = item.config ?? {};
  const configTitle = typeof config.title === "string" ? config.title : "";
  const rangeDays = typeof config.rangeDays === "number" ? config.rangeDays : undefined;

  // Title is committed on blur/Enter (not per-keystroke) — every commit is
  // one undo-history entry, so typing a title shouldn't burn the undo stack
  // one character at a time. Local draft resets whenever the SELECTED TILE
  // changes (the `key={item.i}` on the parent already remounts this
  // component for that, so a plain useState initializer is enough).
  const [titleDraft, setTitleDraft] = useState(configTitle);
  useEffect(() => setTitleDraft(configTitle), [configTitle]);

  const commitTitle = () => {
    const trimmed = titleDraft.trim();
    if (trimmed === configTitle) return;
    const next: Record<string, unknown> = { ...config };
    if (trimmed) next.title = trimmed;
    else delete next.title;
    onSetConfig(item.i, Object.keys(next).length > 0 ? next : undefined);
  };

  const setRange = (value: string) => {
    const preset = RANGE_PRESETS.find((r) => r.value === value);
    const next: Record<string, unknown> = { ...config };
    if (preset?.days) next.rangeDays = preset.days;
    else delete next.rangeDays;
    onSetConfig(item.i, Object.keys(next).length > 0 ? next : undefined);
  };

  const hasViews = (entry?.views?.length ?? 0) > 0;
  const currentView = item.view ?? entry?.defaultView ?? entry?.views?.[0]?.id;

  return (
    <div className="flex flex-col gap-4" data-testid="dashboard-edit-config">
      <div>
        <div className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground">
          &gt; CONFIGURE
        </div>
        <div className="mt-1 font-mono text-[11px] font-semibold uppercase tracking-[0.1em] text-primary">
          {entry?.title ?? item.i}
        </div>
      </div>

      {!entry && (
        <p className="text-[10px] text-muted-foreground">
          This tile's widget kind ("{item.i}") is no longer in the catalog —
          remove it with the × on the tile.
        </p>
      )}

      {/* Title override */}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="tile-config-title" className="text-[10px] uppercase tracking-wide">
          Title
        </Label>
        <Input
          id="tile-config-title"
          data-testid="dashboard-edit-config-title"
          value={titleDraft}
          placeholder={entry?.title ?? item.i}
          onChange={(e) => setTitleDraft(e.target.value)}
          onBlur={commitTitle}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              (e.target as HTMLInputElement).blur();
            }
          }}
        />
      </div>

      {/* View selector — only when the catalog entry offers views */}
      {hasViews && (
        <div className="flex flex-col gap-1.5">
          <Label className="text-[10px] uppercase tracking-wide">View</Label>
          <Select value={currentView} onValueChange={(v: string) => onSetView(item.i, v)}>
            <SelectTrigger data-testid="dashboard-edit-config-view">
              <SelectValue placeholder="View" />
            </SelectTrigger>
            <SelectContent>
              {entry!.views!.map((v) => (
                <SelectItem key={v.id} value={v.id}>
                  {v.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {/* Per-tile range override */}
      <div className="flex flex-col gap-1.5">
        <Label className="text-[10px] uppercase tracking-wide">Date range</Label>
        <Select value={rangeDays ? String(rangeDays) : "inherit"} onValueChange={setRange}>
          <SelectTrigger data-testid="dashboard-edit-config-range">
            <SelectValue placeholder="Dashboard range" />
          </SelectTrigger>
          <SelectContent>
            {RANGE_PRESETS.map((r) => (
              <SelectItem key={r.value} value={r.value}>
                {r.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {/* Visibility toggle */}
      <label
        className="flex items-center justify-between gap-2 rounded border border-border/60 p-2"
        data-testid="dashboard-edit-config-visible"
      >
        <span className="flex flex-col">
          <span className="text-xs font-medium">Visible</span>
          <span className="text-[10px] text-muted-foreground">
            Hidden tiles keep their spot but don't render outside edit mode.
          </span>
        </span>
        <Switch
          checked={!item.hidden}
          onCheckedChange={(v) => onSetHidden(item.i, !v)}
          aria-label="Tile visible"
        />
      </label>
    </div>
  );
}
