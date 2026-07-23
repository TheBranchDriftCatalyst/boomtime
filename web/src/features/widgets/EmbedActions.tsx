import { Link2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { api } from "@/lib/api";
import type { WidgetScope } from "@/types/api";
import { embedSnippets, widgetSvgUrl } from "./catalog";

interface EmbedLinkButtonProps {
  /** Widget kind with a server-rendered SVG twin (see catalog.ts). */
  kind: string;
  scopeType?: WidgetScope;
  scopeRef?: string;
  days?: number;
  theme?: string;
}

// The live-embed half of the per-chart hover cluster (gaka-hsj). Plugs into
// ChartCard's `embedAction` slot on charts whose data has a server-rendered
// widget twin. Clicking mints the scope's widget link (idempotent upsert) and
// copies a Markdown snippet whose URL renders live — the thing you paste into
// a GitHub README. The snapshot button next to it (ChartCard-built-in) covers
// every other chart with a static copy.
export function EmbedLinkButton({
  kind,
  scopeType = "user",
  scopeRef = "",
  days = 30,
  theme = "dark",
}: EmbedLinkButtonProps) {
  async function copyEmbedLink() {
    try {
      const link = await api.getWidgetLink(scopeType, scopeRef);
      const url = widgetSvgUrl(link.widgetBaseUrl, kind, { days, theme });
      await navigator.clipboard.writeText(embedSnippets(url).markdown);
      toast.success("Embed Markdown copied (live, auto-updating)");
    } catch {
      toast.error("Failed to mint the embed link");
    }
  }

  return (
    <Button
      variant="outline"
      size="icon"
      className="h-7 w-7 bg-card/80 backdrop-blur-sm"
      title="Copy embed link (live Markdown for READMEs)"
      aria-label="Copy embed link"
      onClick={copyEmbedLink}
    >
      <Link2 className="h-3.5 w-3.5" />
    </Button>
  );
}
