// CatalogPage — the widget gallery.
//
// A plain, standard app surface (Page POM + catalyst-ui Card cards) whose job
// is to render EVERY widget live so we can see them side-by-side, QA their
// alignment, and (for no-data users / the public page) preview them on sample
// data. Two variants share one component:
//   - "app"    → authed /app/catalog, inside the AppShell; my-data ↔ sample toggle.
//   - "public" → unauthed /catalog, its own minimal shell; sample data only.
//
// Widgets render INLINE (via CatalogWidgetRenderer) so a browser screenshot /
// the Snapshot (print) button captures them — the <object> SVG embed can't be
// rasterized in-page.
import { useMemo, useState } from "react";
import { Camera, Ruler } from "lucide-react";
import { cn } from "@shared/lib/utils";
import { Page } from "@shared/layout/Page";
import {
  CatalogDataProvider,
  useCatalogSource,
  type CatalogSource,
} from "./CatalogDataSource";
import { specForKind } from "@shared/features/widgets/specs";
import { usePublicConfig } from "@shared/lib/usePublicConfig";
import { CATALOG_CATEGORIES, visibleCatalogWidgets } from "./catalogEntries";
import { CatalogWidgetRenderer } from "./CatalogWidgetRenderer";
import { WidgetCatalogCard } from "./WidgetCatalogCard";
import "./catalog.css";

interface CatalogPageProps {
  variant?: "app" | "public";
}

// Card sizing is driven ENTIRELY by the widget spec's `size` (specs.json — the
// single source of truth), so a card matches the widget's true footprint.
// `badge` is the only kind without a spec size (its SVG width is dynamic).
const BADGE_SIZE = { w: 240, h: 60 };
function specSize(kind: string): { w: number; h: number } {
  return specForKind(kind)?.size ?? BADGE_SIZE;
}
/** Column span (in 150px catalog tracks) from the spec width. */
function colsFor(w: number): number {
  return Math.min(6, Math.max(2, Math.round(w / 170)));
}

function CatalogInner({ variant = "app" }: CatalogPageProps) {
  const { source, setSource } = useCatalogSource();
  const [rulers, setRulers] = useState(false);
  const [activeCat, setActiveCat] = useState<string | null>(null);

  // gaka-qcxg: the visible widget slice honors boot-config feature flags — the
  // reading-domain kinds only appear when books_enabled is on (same gate as the
  // Reading overview tab). On the public /catalog page this reads the real
  // server config too (GET /config/public is unauthed), so the gallery matches
  // the deployment's live feature set.
  const { config } = usePublicConfig();
  const widgets = useMemo(() => visibleCatalogWidgets(config), [config]);
  const countIn = (category: string) =>
    widgets.filter((w) => w.category === category).length;

  // The public page has no auth → real data can't load; force sample.
  const effectiveSource: CatalogSource = variant === "public" ? "sample" : source;

  const groups = useMemo(
    () =>
      CATALOG_CATEGORIES.map((category) => ({
        category,
        items: widgets.filter((w) => w.category === category),
      }))
        .filter((g) => g.items.length > 0)
        .filter((g) => !activeCat || g.category === activeCat),
    [activeCat, widgets],
  );

  const total = widgets.length;

  const segBtn = (active: boolean) =>
    cn(
      "px-2.5 py-1 text-xs font-medium transition-colors",
      active
        ? "bg-primary text-primary-foreground"
        : "text-muted-foreground hover:text-foreground",
    );

  const toolBtn = (active?: boolean) =>
    cn(
      "inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs font-medium transition-colors",
      active
        ? "border-primary text-primary"
        : "border-border text-muted-foreground hover:text-foreground",
    );

  const controls = (
    <div className="flex flex-wrap items-center gap-2">
      {variant === "app" && (
        <div
          className="inline-flex overflow-hidden rounded-md border border-border"
          role="group"
          aria-label="Data source"
        >
          <button type="button" onClick={() => setSource("mine")} aria-pressed={source === "mine"} className={segBtn(source === "mine")}>
            My data
          </button>
          <button type="button" onClick={() => setSource("sample")} aria-pressed={source === "sample"} className={cn("border-l border-border", segBtn(source === "sample"))}>
            Sample
          </button>
        </div>
      )}
      <button type="button" onClick={() => setRulers((v) => !v)} aria-pressed={rulers} className={toolBtn(rulers)} title="Toggle alignment guides">
        <Ruler size={13} /> Rulers
      </button>
      <button type="button" onClick={() => window.print()} className={toolBtn()} title="Export a snapshot of the page (print / save as PDF)">
        <Camera size={13} /> Snapshot
      </button>
    </div>
  );

  const body = (
    <div className={cn(rulers && "catalog--rulers")}>
      {/* Category filter */}
      <div className="mb-6 flex flex-wrap gap-2">
        <FilterChip active={!activeCat} onClick={() => setActiveCat(null)} label="All" n={total} />
        {CATALOG_CATEGORIES.map((c) => {
          const n = countIn(c);
          return n > 0 ? (
            <FilterChip key={c} active={activeCat === c} onClick={() => setActiveCat(c)} label={c} n={n} />
          ) : null;
        })}
      </div>

      {groups.map((g) => (
        <section key={g.category} className="mb-9">
          <h2 className="mb-3 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {g.category}
            <span className="text-muted-foreground/50">{g.items.length}</span>
            <span className="ml-1 h-px flex-1 bg-border/60" aria-hidden />
          </h2>
          <div className="catalog-grid">
            {g.items.map((entry) => {
              const sz = specSize(entry.kind);
              return (
                <WidgetCatalogCard
                  key={entry.kind}
                  entry={entry}
                  cols={colsFor(sz.w)}
                  aspect={sz.w / sz.h}
                >
                  <CatalogWidgetRenderer kind={entry.kind} source={effectiveSource} />
                </WidgetCatalogCard>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );

  if (variant === "public") {
    return (
      <div className="min-h-dvh bg-background text-foreground">
        <header className="flex flex-wrap items-center gap-3 border-b border-border px-6 py-4">
          <img src="/boomtime.svg" alt="" aria-hidden className="h-7 w-7 rounded" />
          <div className="mr-auto">
            <h1 className="text-lg font-semibold leading-none">Widget Catalog</h1>
            <p className="mt-1 text-xs text-muted-foreground">
              Every boomtime widget, live on sample data · {total} widgets
            </p>
          </div>
          {controls}
        </header>
        <main className="p-6">{body}</main>
      </div>
    );
  }

  return (
    <Page>
      <Page.Header title="Widget Catalog" subtitle={`${total} widgets · live previews`}>
        {controls}
      </Page.Header>
      <Page.Body>
        <Page.Content>{body}</Page.Content>
      </Page.Body>
    </Page>
  );
}

function FilterChip({ active, onClick, label, n }: { active: boolean; onClick: () => void; label: string; n: number }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "rounded-full border px-3 py-1 text-xs font-medium transition-colors",
        active
          ? "border-primary bg-primary/10 text-primary"
          : "border-border text-muted-foreground hover:text-foreground",
      )}
    >
      {label} <span className="opacity-60">{n}</span>
    </button>
  );
}

export function CatalogPage(props: CatalogPageProps) {
  return (
    <CatalogDataProvider>
      <CatalogInner {...props} />
    </CatalogDataProvider>
  );
}
