// DashboardEditorCard — settings-tab card that lets the owner arrange
// their public dashboard tiles (gaka-keb).
//
// Wired with:
//   - The isolated grid primitive in edit mode (drag handles, resize, X to
//     remove).
//   - A DB-backed StorageAdapter that PUTs to
//     /api/v1/users/current/dashboard/public_profile on every layout change.
//   - A palette on the right showing widgets not currently in the layout;
//     click "+" to add.
//   - A "Reset to defaults" button that DELETEs the persisted layout so the
//     next public-profile fetch falls back to defaults.
//
// Only mounted when the public profile is enabled (parent gates on the
// profile query).
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { PanelRightClose, PanelRightOpen, Plus, RotateCcw } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent, CardHeader } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import {
  DraggableGridLayout,
  memoryAdapter,
  type GridLayoutItem,
  type WidgetInstance,
} from "@/lib/grid";
import { WIDGET_CATALOG } from "@/features/widgets/catalog";
import { WidgetRenderer } from "@/features/widgets/renderers/WidgetRenderer";
import { PUBLIC_PROFILE_DEFAULT_LAYOUT } from "@/features/publicprofile/defaults";
import "@/features/publicprofile/hacker.css";
import type { PublicDashboardPayload } from "@/types/stats";

// Fake preview payload for the editor. We can't render live user data here
// without a stats round-trip; a deterministic tiny payload is enough for
// the user to see layout — the shipped dashboard fetches the real one.
const PREVIEW_PAYLOAD: PublicDashboardPayload = {
  username: "you",
  startDate: new Date(Date.now() - 60 * 86_400_000).toISOString(),
  endDate: new Date().toISOString(),
  totalSeconds: 12 * 3600,
  dailyAvg: 45 * 60,
  dailyTotal: Array.from({ length: 60 }, (_, i) => (i % 4 === 0 ? 0 : (i * 137) % 5400)),
  projects: [
    { name: "boomtime", totalSeconds: 5 * 3600, totalPct: 41.7, totalDaily: [], pctDaily: [] },
    { name: "catalyst-ui", totalSeconds: 4 * 3600, totalPct: 33.3, totalDaily: [], pctDaily: [] },
    { name: "hakboard", totalSeconds: 3 * 3600, totalPct: 25.0, totalDaily: [], pctDaily: [] },
  ],
  languages: [
    { name: "TypeScript", totalSeconds: 6 * 3600, totalPct: 50, totalDaily: [], pctDaily: [] },
    { name: "Go", totalSeconds: 4 * 3600, totalPct: 33, totalDaily: [], pctDaily: [] },
    { name: "CSS", totalSeconds: 2 * 3600, totalPct: 17, totalDaily: [], pctDaily: [] },
  ],
  editors: [
    { name: "Neovim", totalSeconds: 8 * 3600, totalPct: 67, totalDaily: [], pctDaily: [] },
    { name: "VS Code", totalSeconds: 4 * 3600, totalPct: 33, totalDaily: [], pctDaily: [] },
  ],
  platforms: [
    { name: "Darwin", totalSeconds: 12 * 3600, totalPct: 100, totalDaily: [], pctDaily: [] },
  ],
  categories: [
    { name: "coding", totalSeconds: 10 * 3600, totalPct: 83, totalDaily: [], pctDaily: [] },
    { name: "debugging", totalSeconds: 2 * 3600, totalPct: 17, totalDaily: [], pctDaily: [] },
  ],
  punchcard: {
    cells: [],
    maxSeconds: 3600,
    totalSeconds: 12 * 3600,
  },
};

export function DashboardEditorCard() {
  const qc = useQueryClient();

  // Load persisted layout (or 404 → fall back to defaults).
  const { data: saved, isLoading } = useQuery({
    queryKey: qk.dashboardLayout("public_profile"),
    queryFn: async () => {
      try {
        return await api.getDashboardLayout("public_profile");
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
  });

  const seed = useMemo<GridLayoutItem[]>(() => {
    const persisted = (saved?.layout as { widgets?: GridLayoutItem[] } | undefined)?.widgets;
    if (persisted && persisted.length > 0) return persisted;
    return PUBLIC_PROFILE_DEFAULT_LAYOUT;
  }, [saved]);

  // Local editing state — draft state that only PUTs when the user hits
  // "Commit layout". This makes drag/resize feel responsive without one
  // network hit per pixel.
  const [draft, setDraft] = useState<GridLayoutItem[]>(seed);
  useEffect(() => setDraft(seed), [seed]);

  const allProfileEntries = useMemo(
    () => WIDGET_CATALOG.filter((e) => (e.dashboardScopes ?? []).includes("profile")),
    [],
  );

  const inLayout = useMemo(() => new Set(draft.map((w) => w.i)), [draft]);
  const paletteEntries = useMemo(
    () => allProfileEntries.filter((e) => !inLayout.has(e.kind)),
    [allProfileEntries, inLayout],
  );

  const instances: WidgetInstance[] = useMemo(
    () =>
      allProfileEntries.map((e) => ({
        key: e.kind,
        displayName: e.title,
        defaultLayout: e.defaultLayout,
        views: e.views,
        defaultView: e.defaultView,
        render: ({ view }) => (
          <WidgetRenderer kind={e.kind} view={view} data={PREVIEW_PAYLOAD} />
        ),
      })),
    [allProfileEntries],
  );

  // In-memory adapter for the primitive — it saves to draft state so we
  // don't PUT on every drag.
  const storage = useMemo(
    () =>
      memoryAdapter(seed),
    // Rebuild the adapter when the seed identity changes (fresh load).
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [seed],
  );

  const [saving, setSaving] = useState(false);
  const [committed, setCommitted] = useState(false);
  // Palette expand/collapse — persisted so the state survives tab switches
  // and reloads. Default: expanded (the palette is where you discover
  // widgets to add; new users need it open to know what exists).
  const [paletteCollapsed, setPaletteCollapsed] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    try {
      return window.localStorage.getItem("dashboard-editor:palette-collapsed") === "1";
    } catch {
      return false;
    }
  });
  useEffect(() => {
    try {
      window.localStorage.setItem(
        "dashboard-editor:palette-collapsed",
        paletteCollapsed ? "1" : "0",
      );
    } catch {
      // localStorage disabled — state stays in-memory only, that's fine.
    }
  }, [paletteCollapsed]);
  useEffect(() => {
    // Poll the adapter's memory when it changes — cheap since it's local.
    // The primitive fires save on every layout mutation; mirror that into
    // draft here for the palette + commit button.
    void (async () => {
      const cur = await storage.load();
      if (cur) setDraft(cur);
    })();
  }, [storage]);

  const handleAdd = (kind: string) => {
    const entry = allProfileEntries.find((e) => e.kind === kind);
    if (!entry) return;
    const w = entry.defaultLayout?.w ?? 6;
    const h = entry.defaultLayout?.h ?? 3;
    // Compute a next-empty-row Y (append at bottom).
    const maxY = draft.reduce((m, it) => Math.max(m, it.y + it.h), 0);
    const next: GridLayoutItem[] = [
      ...draft,
      { i: kind, x: 0, y: maxY, w, h, view: entry.defaultView ?? null },
    ];
    setDraft(next);
    void storage.save(next);
  };

  const commit = async () => {
    setSaving(true);
    try {
      await api.putDashboardLayout("public_profile", { cols: 12, widgets: draft });
      toast.success("Public dashboard layout saved");
      setCommitted(true);
      qc.invalidateQueries({ queryKey: qk.dashboardLayout("public_profile") });
      // Invalidate any /p/:slug queries (all slugs) so an in-flight
      // preview tab picks up the new layout on refetch.
      qc.invalidateQueries({ queryKey: ["public-dashboard"] });
      setTimeout(() => setCommitted(false), 1500);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  const reset = async () => {
    setSaving(true);
    try {
      await api.deleteDashboardLayout("public_profile");
      toast.success("Layout reset to defaults");
      setDraft(PUBLIC_PROFILE_DEFAULT_LAYOUT);
      qc.invalidateQueries({ queryKey: qk.dashboardLayout("public_profile") });
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Reset failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card data-testid="dashboard-editor-card">
      <CardHeader className="p-4 pb-0">
        <h2 className="text-lg font-semibold">Public dashboard layout</h2>
        <p className="text-sm text-muted-foreground">
          Drag and resize the tiles on your public profile page. Add widgets from
          the palette; click the × on a tile to remove it. Nothing saves until
          you commit.
        </p>
      </CardHeader>
      <CardContent className="p-4">
        {isLoading ? (
          <div className="py-8 text-center text-muted-foreground">Loading…</div>
        ) : (
          <div
            className={
              paletteCollapsed
                ? "grid gap-4 lg:grid-cols-[1fr_44px]"
                : "grid gap-4 lg:grid-cols-[1fr_340px]"
            }
          >
            <div className="public-dashboard rounded border border-border p-2">
              <DraggableGridLayout
                instances={instances}
                storage={storage}
                editable
                cols={12}
                seedLayout={draft}
              />
            </div>
            <aside
              className={
                paletteCollapsed
                  ? "flex flex-col items-center gap-2 rounded border border-border p-1.5"
                  : "flex max-h-[720px] flex-col gap-2 overflow-y-auto rounded border border-border p-3"
              }
              aria-label="Widget palette"
              data-collapsed={paletteCollapsed || undefined}
            >
              <div
                className={
                  paletteCollapsed
                    ? "flex w-full justify-center"
                    : "flex items-center justify-between"
                }
              >
                {!paletteCollapsed && (
                  <div className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground">
                    &gt; PALETTE ({paletteEntries.length})
                  </div>
                )}
                <button
                  type="button"
                  onClick={() => setPaletteCollapsed((v) => !v)}
                  className="rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"
                  aria-label={paletteCollapsed ? "Expand palette" : "Collapse palette"}
                  aria-expanded={!paletteCollapsed}
                  title={paletteCollapsed ? `Expand palette (${paletteEntries.length})` : "Collapse palette"}
                  data-testid="dashboard-editor-palette-toggle"
                >
                  {paletteCollapsed ? (
                    <PanelRightOpen size={14} />
                  ) : (
                    <PanelRightClose size={14} />
                  )}
                </button>
              </div>
              {paletteCollapsed ? (
                <div
                  className="font-mono text-[9px] tabular-nums text-muted-foreground"
                  aria-hidden
                >
                  {paletteEntries.length}
                </div>
              ) : (
                <>
                  {paletteEntries.length === 0 && (
                    <div className="text-xs text-muted-foreground">
                      All widgets are in your layout.
                    </div>
                  )}
                  {paletteEntries.map((e) => (
                    <button
                      key={e.kind}
                      type="button"
                      onClick={() => handleAdd(e.kind)}
                      className="flex items-start justify-between gap-2 rounded border border-border/60 p-2 text-left text-xs hover:border-primary/60"
                      data-testid={`palette-add-${e.kind}`}
                    >
                      <div>
                        <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.1em] text-primary">
                          {e.title}
                        </div>
                        <div className="mt-1 text-[10px] text-muted-foreground">
                          {e.description}
                        </div>
                      </div>
                      <Plus size={14} />
                    </button>
                  ))}
                </>
              )}
            </aside>
          </div>
        )}
        <div className="mt-4 flex items-center gap-2">
          <Button
            type="button"
            onClick={commit}
            disabled={saving}
            data-testid="dashboard-editor-commit"
            className="font-mono uppercase tracking-[0.15em]"
          >
            {saving ? "SAVING…" : committed ? "[ COMMITTED ]" : "[ COMMIT LAYOUT ]"}
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={reset}
            disabled={saving}
            data-testid="dashboard-editor-reset"
            className="font-mono uppercase tracking-[0.15em]"
          >
            <RotateCcw size={12} className="mr-1" />
            RESET
          </Button>
          <span className="text-xs text-muted-foreground">
            {draft.length} widget{draft.length === 1 ? "" : "s"} placed
          </span>
        </div>
      </CardContent>
    </Card>
  );
}
