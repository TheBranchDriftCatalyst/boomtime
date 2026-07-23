import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { embedSnippets, widgetSvgUrl, type WidgetCatalogEntry } from "./catalog";

interface WidgetCardProps {
  entry: WidgetCatalogEntry;
  /** The minted public base URL (…/widget/svg/<uuid>). */
  baseUrl: string;
  days: number;
  theme: string;
}

// One catalog entry inside the Widgets panel: live preview (the endpoint is
// public, so a plain <img> works) + one copy row per embed format.
export function WidgetCard({ entry, baseUrl, days, theme }: WidgetCardProps) {
  const url = widgetSvgUrl(baseUrl, entry.kind, { days, theme });
  const snippets = embedSnippets(url);
  const [copied, setCopied] = useState<string | null>(null);

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(label);
      setTimeout(() => setCopied(null), 1500);
      toast.success(`${label} copied to clipboard`);
    } catch {
      toast.error("Copy failed");
    }
  }

  const rows: Array<{ label: string; value: string }> = [
    { label: "Image URL", value: snippets.url },
    { label: "Markdown", value: snippets.markdown },
    { label: "HTML", value: snippets.html },
  ];

  return (
    <div className="rounded-lg border border-border p-4 space-y-3">
      <div>
        <div className="font-medium">{entry.title}</div>
        <div className="text-xs text-muted-foreground">{entry.description}</div>
      </div>
      <div className="overflow-x-auto rounded bg-background/40 p-2">
        {/* <object> (not <img>) so the SVG's native <title> hover tooltips and
            :hover styles work in the preview; key forces a re-fetch when
            range/theme change. pointer events stay inside the object. */}
        <object
          key={url}
          type="image/svg+xml"
          data={url}
          aria-label={`${entry.title} preview`}
          className="max-w-full"
        />
      </div>
      <div className="space-y-1">
        {rows.map((row) => (
          <div key={row.label} className="flex items-center gap-2">
            <span className="w-20 shrink-0 text-xs text-muted-foreground">
              {row.label}
            </span>
            <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 text-xs">
              {row.value}
            </code>
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7 shrink-0"
              aria-label={`Copy ${row.label} for ${entry.title}`}
              onClick={() => copy(row.label, row.value)}
            >
              {copied === row.label ? (
                <Check className="h-3.5 w-3.5" />
              ) : (
                <Copy className="h-3.5 w-3.5" />
              )}
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}
