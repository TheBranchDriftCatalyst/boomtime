// WidgetCatalogCard — one entry in the widget catalog.
//
// Uses the app's STANDARD card chrome (catalyst-ui <Card>, same as ChartCard)
// so the catalog looks like every other page — it is a utility for viewing and
// QA-ing widgets, not a new dashboard skin. It frames a LIVE render (passed as
// `children`) in a fixed-height stage so every widget is compared in the same
// box, and offers copy-embed actions for embeddable kinds.
import { useState, type ReactNode } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Copy, Check, Code2 } from "lucide-react";
import { toast } from "sonner";
import { embedSnippets } from "@shared/features/widgets/catalog";

/** The subset of catalog metadata a card renders. Structural — matches the
 * entries produced by catalogEntries.ts (CATALOG_WIDGETS). */
export interface CatalogCardEntry {
  kind: string;
  title: string;
  description: string;
  target: "both" | "fe-only";
  category: string;
  primitives?: string[];
  embeddable: boolean;
}

export interface WidgetCatalogCardProps {
  entry: CatalogCardEntry;
  /** Live embeddable SVG URL for the copy actions (embeddable kinds only). */
  embedUrl?: string;
  /** Grid column span (in catalog track units) — derived from the spec size. */
  cols?: number;
  /** Live-preview aspect ratio (w/h) — from the spec size. */
  aspect?: number;
  /** The live, inline-rendered widget. */
  children: ReactNode;
}

export function WidgetCatalogCard({ entry, embedUrl, cols, aspect, children }: WidgetCatalogCardProps) {
  const [copied, setCopied] = useState<null | "md" | "html">(null);

  const copy = async (which: "md" | "html") => {
    if (!embedUrl) return;
    const snip = embedSnippets(embedUrl);
    const text = which === "md" ? snip.markdown : snip.html;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(which);
      toast.success(which === "md" ? "Markdown embed copied" : "HTML embed copied");
      window.setTimeout(() => setCopied(null), 1400);
    } catch {
      toast.error("Couldn't copy to clipboard");
    }
  };

  return (
    <Card
      className="catalog-card group relative flex flex-col"
      data-kind={entry.kind}
      data-target={entry.target}
      style={{
        ...(cols ? { ["--catalog-cols" as string]: cols } : {}),
        ...(aspect ? { ["--catalog-aspect" as string]: aspect } : {}),
      }}
    >
      <CardHeader className="flex flex-row items-start justify-between space-y-0 pb-2">
        <div className="flex min-w-0 flex-col gap-0.5">
          <CardTitle className="text-sm font-semibold text-foreground">{entry.title}</CardTitle>
          <code className="truncate text-[10px] tracking-wider text-muted-foreground/70">{entry.kind}</code>
        </div>
        <span
          className={
            "shrink-0 rounded border px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider " +
            (entry.embeddable
              ? "border-primary/40 text-primary"
              : "border-border text-muted-foreground")
          }
          title={entry.embeddable ? "Renders as an SVG embed and in-app" : "In-app render only"}
        >
          {entry.embeddable ? "embed" : "in-app"}
        </span>
      </CardHeader>

      <CardContent className="flex flex-1 flex-col gap-2 pt-0">
        <div className="catalog-stage rounded-md border border-border/60 bg-muted/20 p-2">
          {children}
        </div>
        <div className="flex items-start gap-2">
          <p className="line-clamp-2 flex-1 text-[11px] leading-snug text-muted-foreground" title={entry.description}>
            {entry.description}
          </p>
          {entry.embeddable && embedUrl && (
            <div className="flex shrink-0 gap-1">
              <button
                type="button"
                onClick={() => copy("md")}
                aria-label="Copy Markdown embed"
                title="Copy Markdown embed"
                className={
                  "inline-flex h-6 w-6 items-center justify-center rounded border transition-colors " +
                  (copied === "md"
                    ? "border-emerald-500 text-emerald-500"
                    : "border-border text-muted-foreground hover:border-primary hover:text-primary")
                }
              >
                {copied === "md" ? <Check size={12} /> : <Copy size={12} />}
              </button>
              <button
                type="button"
                onClick={() => copy("html")}
                aria-label="Copy HTML embed"
                title="Copy HTML embed"
                className={
                  "inline-flex h-6 w-6 items-center justify-center rounded border transition-colors " +
                  (copied === "html"
                    ? "border-emerald-500 text-emerald-500"
                    : "border-border text-muted-foreground hover:border-primary hover:text-primary")
                }
              >
                {copied === "html" ? <Check size={12} /> : <Code2 size={12} />}
              </button>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
