import { useMemo, useState } from "react";
import { Check, Copy, Wand2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { cn } from "@/lib/utils";
import type { WidgetScope } from "@/types/api";
import { useWidgetLink } from "./useWidgetLink";
import {
  LAYOUT_CATALOG,
  PANEL_CATALOG,
  customWidgetUrl,
  panelCount,
  type WidgetLayout,
  type WidgetPanelKind,
  type WidgetDef,
} from "./builder";

interface WidgetBuilderProps {
  scopeType: WidgetScope;
  scopeRef?: string;
}

const DEFAULT_PANELS: WidgetPanelKind[] = [
  "calendar",
  "top-langs",
  "grade",
];

// The Widget Builder Sheet (gaka-567). Layout + one primitive per panel + a
// title; the spec is base64-encoded into the URL, previewed live via
// <object>, and copyable as Markdown when you like it. No saved-def table
// for v1 — the whole composition lives in the URL.
export function WidgetBuilder({ scopeType, scopeRef = "" }: WidgetBuilderProps) {
  const [open, setOpen] = useState(false);
  const [layout, setLayout] = useState<WidgetLayout>("3-panel-h");
  const [panels, setPanels] = useState<WidgetPanelKind[]>(DEFAULT_PANELS);
  const [title, setTitle] = useState("Coding profile");
  const [days, setDays] = useState(30);
  const [theme, setTheme] = useState("dark");
  const [copied, setCopied] = useState<string | null>(null);

  const link = useWidgetLink(scopeType, scopeRef, open);

  // Truncate/pad panel selection to the layout's slot count on layout change.
  function setLayoutAndReshape(next: WidgetLayout) {
    setLayout(next);
    const n = panelCount(next);
    setPanels((prev) => {
      const out = [...prev];
      while (out.length < n) out.push(DEFAULT_PANELS[out.length % DEFAULT_PANELS.length]);
      return out.slice(0, n);
    });
  }

  const def: WidgetDef = useMemo(
    () => ({
      layout,
      title: title.trim() || undefined,
      panels: panels.map((k) => ({ kind: k })),
    }),
    [layout, title, panels],
  );

  const url = link.data
    ? customWidgetUrl(link.data.widgetBaseUrl, def, { days, theme })
    : "";
  const markdown = url ? `![${title || "Widget"}](${url})` : "";

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(label);
      setTimeout(() => setCopied(null), 1500);
      toast.success(`${label} copied`);
    } catch {
      toast.error("Copy failed");
    }
  }

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="outline" size="sm" aria-label="Open widget builder">
          <Wand2 className="mr-1.5 h-4 w-4" />
          Build
        </Button>
      </SheetTrigger>
      <SheetContent className="w-[52vw] min-w-[560px]">
        <SheetHeader>
          <SheetTitle>Widget builder</SheetTitle>
          <SheetDescription>
            Compose your own widget from primitives. Pick a layout, drop a
            primitive into each panel, copy the Markdown when it looks right.
            (The whole composition lives in the URL — no server-side save
            needed.)
          </SheetDescription>
        </SheetHeader>

        <div className="space-y-4">
          <div>
            <Label className="mb-1 block text-xs">Title</Label>
            <Input value={title} onChange={(e) => setTitle(e.target.value)} />
          </div>

          <div>
            <Label className="mb-2 block text-xs">Layout</Label>
            <div className="flex flex-wrap gap-2">
              {LAYOUT_CATALOG.map((l) => (
                <Button
                  key={l.layout}
                  variant={layout === l.layout ? "default" : "outline"}
                  size="sm"
                  onClick={() => setLayoutAndReshape(l.layout)}
                >
                  {l.label}
                </Button>
              ))}
            </div>
          </div>

          <div>
            <Label className="mb-2 block text-xs">Panels</Label>
            <div className="space-y-2">
              {panels.map((kind, i) => (
                <PanelPicker
                  key={i}
                  slot={i + 1}
                  selected={kind}
                  onChange={(k) =>
                    setPanels((prev) =>
                      prev.map((p, j) => (i === j ? k : p)),
                    )
                  }
                />
              ))}
            </div>
          </div>

          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1">
              {[7, 30, 90, 366].map((d) => (
                <Button
                  key={d}
                  variant={days === d ? "default" : "outline"}
                  size="sm"
                  onClick={() => setDays(d)}
                >
                  {d === 366 ? "1y" : `${d}d`}
                </Button>
              ))}
            </div>
            <div className="flex items-center gap-1">
              {(["dark", "light"] as const).map((t) => (
                <Button
                  key={t}
                  variant={theme === t ? "default" : "outline"}
                  size="sm"
                  onClick={() => setTheme(t)}
                >
                  {t}
                </Button>
              ))}
            </div>
          </div>
        </div>

        <div className="mt-2 space-y-3">
          <Label className="block text-xs">Live preview</Label>
          {url ? (
            <div className="overflow-x-auto rounded border border-border bg-background/40 p-2">
              <object
                key={url}
                type="image/svg+xml"
                data={url}
                aria-label="Custom widget preview"
                className="max-w-full"
              />
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">Minting link…</p>
          )}

          {url && (
            <div className="space-y-1">
              {(
                [
                  { label: "Markdown", value: markdown },
                  { label: "Image URL", value: url },
                ] as const
              ).map((row) => (
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
                    aria-label={`Copy ${row.label}`}
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
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function PanelPicker({
  slot,
  selected,
  onChange,
}: {
  slot: number;
  selected: WidgetPanelKind;
  onChange: (k: WidgetPanelKind) => void;
}) {
  return (
    <div className="rounded border border-border p-2">
      <div className="mb-1 text-xs text-muted-foreground">Panel {slot}</div>
      <div className="flex flex-wrap gap-1">
        {PANEL_CATALOG.map((p) => (
          <button
            key={p.kind}
            type="button"
            onClick={() => onChange(p.kind)}
            title={p.hint}
            className={cn(
              "rounded border px-2 py-1 text-xs transition-colors",
              p.kind === selected
                ? "border-primary bg-primary text-primary-foreground"
                : "border-border text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            {p.label}
          </button>
        ))}
      </div>
    </div>
  );
}
