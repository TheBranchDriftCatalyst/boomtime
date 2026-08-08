// ProfileEditor — inline editor rendered on top of /p/:slug when the
// caller owns the profile and has flipped the mode toggle to "Edit"
// (gaka-ie3).
//
// Design goals:
//   - Reuse the same widget catalog + renderers + grid primitive the public
//     page uses, so the WYSIWYG "edit surface" IS the shipped page.
//   - Draft-then-save: drag/resize/palette-add mutate local state only. An
//     explicit Save button commits via PUT /dashboard/public_profile.
//     Discard reverts to the last server value.
//   - Guard against accidental loss: browser beforeunload prompt while
//     dirty; react-router useBlocker prompt on in-app navigation.
//   - Real user data — unlike the old DashboardEditorCard in Settings which
//     rendered a synthetic PREVIEW_PAYLOAD, this editor renders the actual
//     public dashboard payload for the caller so the edit surface matches
//     what visitors see, byte-for-byte.
import { useEffect, useMemo, useState } from "react";
import { useBlocker } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { PanelRightClose, PanelRightOpen, Plus, RotateCcw, Save } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
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
import { DossierControls, ReclassifyOverlay } from "./ProfileChrome";
import { useProfileRange } from "./profileRange";
import { PUBLIC_PROFILE_DEFAULT_LAYOUT } from "./defaults";
import "./hacker.css";
import "./arasaka.css";
import "./dossier.css";

/** Deep-ish equality check used for the "is the draft dirty?" gate. The
 * layout is a small array (<20 entries) of primitive-valued items, so
 * JSON.stringify is fine — cheaper than a hand-rolled deep-equal, and
 * the ordering is stable because both sides come from the same source. */
function layoutsEqual(a: GridLayoutItem[], b: GridLayoutItem[]): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export interface ProfileEditorProps {
  slug: string;
}

export function ProfileEditor({ slug }: ProfileEditorProps) {
  const qc = useQueryClient();

  // Reuse the public dashboard payload so the editor renders REAL widget
  // data (not a synthetic PREVIEW). Shares qk.publicDashboard(slug) with
  // the PublicDashboard route so preview<->edit toggling reuses the same
  // network round-trip.
  // gaka-174.7: match the read view — the selected window drives the editor's
  // preview payload too, so an owner sees the layout against the same range.
  const [rangeDays] = useProfileRange();
  const { data: payload, isLoading: payloadLoading } = useQuery({
    queryKey: [...qk.publicDashboard(slug), rangeDays],
    queryFn: () => api.getPublicDashboard(slug, rangeDays),
    enabled: !!slug,
    retry: false,
  });

  // Server-side truth: the persisted layout, or null (=> default). Kept
  // separate from the draft so Discard can revert to it.
  const serverLayout = useMemo<GridLayoutItem[]>(() => {
    const persisted = (payload?.layout as { widgets?: GridLayoutItem[] } | undefined)
      ?.widgets;
    if (persisted && persisted.length > 0) return persisted;
    return PUBLIC_PROFILE_DEFAULT_LAYOUT;
  }, [payload]);

  const [draft, setDraft] = useState<GridLayoutItem[]>(serverLayout);

  // On a fresh server payload (e.g. right after a successful save
  // invalidates the query), re-seed the draft so the two match — otherwise
  // "dirty" would stay true forever after a save.
  useEffect(() => {
    setDraft(serverLayout);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverLayout]);

  const dirty = !layoutsEqual(draft, serverLayout);

  // Widgets scoped to the profile page. `inLayout` narrows the palette to
  // widgets NOT currently placed.
  const allProfileEntries = useMemo(
    () => WIDGET_CATALOG.filter((e) => (e.dashboardScopes ?? []).includes("profile")),
    [],
  );
  const inLayout = useMemo(() => new Set(draft.map((w) => w.i)), [draft]);
  const paletteEntries = useMemo(
    () => allProfileEntries.filter((e) => !inLayout.has(e.kind)),
    [allProfileEntries, inLayout],
  );

  // Wrap the WidgetRenderer as WidgetInstances. The renderer uses REAL
  // payload data; if the fetch is still in flight `payload` is undefined
  // and we render a stub payload so the grid can still mount.
  const instances: WidgetInstance[] = useMemo(
    () =>
      allProfileEntries.map((e) => ({
        key: e.kind,
        displayName: e.title,
        defaultLayout: e.defaultLayout,
        views: e.views,
        defaultView: e.defaultView,
        render: ({ view }) =>
          payload ? (
            <WidgetRenderer kind={e.kind} view={view} data={payload} slug={slug} />
          ) : null,
      })),
    [allProfileEntries, payload, slug],
  );

  // Memory-backed storage adapter — the primitive fires save() on every
  // drag; we mirror that into the draft. Nothing persists to the DB until
  // the operator clicks Save.
  const storage = useMemo(
    () => memoryAdapter(draft),
    // Only rebuild when the SERVER layout identity changes (e.g. reset).
    // Rebuilding on every draft mutation would create adapter thrash and
    // trigger the primitive's mount effect on every drag.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [serverLayout],
  );

  // The primitive calls storage.save on drag; sync into our draft too so
  // "dirty" and the palette recompute. A cheap poll after mount catches
  // the initial merge; subsequent saves route through onLayoutSave.
  useEffect(() => {
    let alive = true;
    void (async () => {
      const cur = await storage.load();
      if (alive && cur) setDraft(cur);
    })();
    return () => {
      alive = false;
    };
  }, [storage]);

  // Override the storage.save to also update our React state (the memory
  // adapter's own state is already updated by the primitive's call). This
  // is the only wiring the primitive needs to feed our draft.
  const wrappedStorage = useMemo(
    () => ({
      load: storage.load,
      save: async (next: GridLayoutItem[]) => {
        await storage.save(next);
        setDraft(next);
      },
    }),
    [storage],
  );

  // ---- Save / Discard ----
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!dirty) return;
    setSaving(true);
    try {
      await api.putDashboardLayout("public_profile", {
        cols: 12,
        widgets: draft,
      });
      toast.success("Public dashboard layout saved");
      // Invalidate BOTH the public dashboard payload (so the preview mode
      // + any /p/:slug tab picks it up) and the dashboard-layout scope key
      // (so Settings-side consumers, if any linger, stay honest).
      qc.invalidateQueries({ queryKey: qk.publicDashboard(slug) });
      qc.invalidateQueries({ queryKey: qk.dashboardLayout("public_profile") });
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  const discard = () => {
    setDraft(serverLayout);
  };

  const resetToDefaults = async () => {
    if (
      !window.confirm(
        "Reset to the default layout? This deletes your saved arrangement.",
      )
    ) {
      return;
    }
    setSaving(true);
    try {
      await api.deleteDashboardLayout("public_profile");
      toast.success("Layout reset to defaults");
      qc.invalidateQueries({ queryKey: qk.publicDashboard(slug) });
      qc.invalidateQueries({ queryKey: qk.dashboardLayout("public_profile") });
      setDraft(PUBLIC_PROFILE_DEFAULT_LAYOUT);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Reset failed");
    } finally {
      setSaving(false);
    }
  };

  // ---- Palette ----
  const [paletteCollapsed, setPaletteCollapsed] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    try {
      return window.localStorage.getItem("profile-editor:palette-collapsed") === "1";
    } catch {
      return false;
    }
  });
  useEffect(() => {
    try {
      window.localStorage.setItem(
        "profile-editor:palette-collapsed",
        paletteCollapsed ? "1" : "0",
      );
    } catch {
      /* localStorage disabled */
    }
  }, [paletteCollapsed]);

  const handleAdd = (kind: string) => {
    const entry = allProfileEntries.find((e) => e.kind === kind);
    if (!entry) return;
    const w = entry.defaultLayout?.w ?? 6;
    const h = entry.defaultLayout?.h ?? 3;
    const maxY = draft.reduce((m, it) => Math.max(m, it.y + it.h), 0);
    const next: GridLayoutItem[] = [
      ...draft,
      { i: kind, x: 0, y: maxY, w, h, view: entry.defaultView ?? null },
    ];
    setDraft(next);
    void wrappedStorage.save(next);
  };

  // ---- Dirty guards ----
  // Browser tab close / reload / typed URL: beforeunload prompts the user
  // if the draft is dirty. Only registered while dirty so a clean editor
  // doesn't fire the "leave page?" dialog on plain page reloads.
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // Legacy contract — some browsers require returnValue to be set.
      e.returnValue = "";
      return "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  // In-app navigation guard: useBlocker intercepts every react-router
  // navigation (Link click, useNavigate, browser back/forward). When
  // the draft is dirty and the user is trying to move to a different
  // pathname, we prompt via window.confirm. Deny -> stay put; accept
  // -> proceed. Requires the data router — see main.tsx (the app was
  // migrated to createBrowserRouter + RouterProvider in gaka-ie3 to
  // unlock this API).
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    const ok = window.confirm(
      "You have unsaved layout changes. Leave without saving?",
    );
    if (ok) blocker.proceed?.();
    else blocker.reset?.();
  }, [blocker]);

  if (payloadLoading || !payload) {
    return (
      <div className="flex h-[60vh] items-center justify-center">
        <Spinner />
      </div>
    );
  }

  return (
    <div
      className="public-dashboard mx-auto max-w-7xl px-4 pt-20"
      data-testid="profile-editor"
    >
      <header className="mb-3 flex items-baseline justify-between">
        <div>
          <h1 className="text-lg font-semibold">Edit public dashboard</h1>
          <p className="text-xs text-muted-foreground">
            Drag tiles to move, drag corners to resize, × to remove. Add widgets
            from the palette. Changes save only when you click Save.
          </p>
        </div>
      </header>

      <div
        className={
          paletteCollapsed
            ? "grid gap-4 lg:grid-cols-[1fr_44px]"
            : "grid gap-4 lg:grid-cols-[1fr_340px]"
        }
      >
        <div className="rounded border border-border p-2">
          <DraggableGridLayout
            instances={instances}
            storage={wrappedStorage}
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
              title={
                paletteCollapsed
                  ? `Expand palette (${paletteEntries.length})`
                  : "Collapse palette"
              }
              data-testid="profile-editor-palette-toggle"
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
                  data-testid={`profile-editor-palette-add-${e.kind}`}
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

      {/* Floating Save / Discard chrome — bottom-right, sticks in view
          while the operator scrolls the tile grid. Disabled buttons when
          the draft matches the server (nothing to save/revert). */}
      <div
        className="fixed bottom-4 right-4 z-40 flex items-center gap-2 rounded-lg border border-border bg-background/95 p-2 shadow-xl backdrop-blur"
        data-testid="profile-editor-save-chrome"
      >
        <span
          className="pl-2 pr-1 font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground"
          data-testid="profile-editor-dirty-indicator"
          data-dirty={dirty || undefined}
        >
          {dirty ? "unsaved changes" : "saved"}
        </span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={resetToDefaults}
          disabled={saving}
          data-testid="profile-editor-reset"
          title="Reset to default layout"
        >
          <RotateCcw size={12} className="mr-1" />
          Reset
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={discard}
          disabled={saving || !dirty}
          data-testid="profile-editor-discard"
        >
          Discard
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={save}
          disabled={saving || !dirty}
          data-testid="profile-editor-save"
        >
          <Save size={12} className="mr-1" />
          {saving ? "Saving…" : "Save"}
        </Button>
      </div>

      {/* gaka-174.2: theme control (bottom-left so it clears the Save chrome)
       * + reclassify sweep, so owners can preview dossier skins while editing. */}
      <DossierControls placement="bl" />
      <ReclassifyOverlay />
    </div>
  );
}
