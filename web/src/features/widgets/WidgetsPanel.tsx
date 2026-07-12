import { useState } from "react";
import { LayoutGrid } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import type { WidgetScope } from "@/types/api";
import { catalogFor } from "./catalog";
import { useWidgetLink } from "./useWidgetLink";
import { WidgetBuilder } from "./WidgetBuilder";
import { WidgetCard } from "./WidgetCard";

interface WidgetsPanelProps {
  /** Page scope: Overview = user, project detail = project, Space = space. */
  scopeType: WidgetScope;
  /** Project name or space id; empty for user scope. */
  scopeRef?: string;
}

const RANGE_CHOICES = [7, 30, 90, 366] as const;

// The Widgets side panel — the discovery/front-door UX for embeddable
// widgets. Opens from the page toolbar; lists the scope's catalog with live
// previews and copyable embed snippets. The widget link is minted lazily on
// first open (the Sheet mounts its content on open).
export function WidgetsPanel({ scopeType, scopeRef = "" }: WidgetsPanelProps) {
  const [open, setOpen] = useState(false);
  const [days, setDays] = useState<number>(30);
  const [theme, setTheme] = useState<string>("dark");
  const link = useWidgetLink(scopeType, scopeRef, open);

  const entries = catalogFor(scopeType);

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="outline" size="sm" aria-label="Open widgets panel">
          <LayoutGrid className="mr-1.5 h-4 w-4" />
          Widgets
        </Button>
      </SheetTrigger>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>Embeddable widgets</SheetTitle>
          <SheetDescription>
            Live SVG cards for your GitHub README, blog or site. Copy a snippet
            — it stays up to date. (iframes don&apos;t work in GitHub READMEs;
            use Markdown or the image URL there.)
          </SheetDescription>
        </SheetHeader>
        <div>
          <WidgetBuilder scopeType={scopeType} scopeRef={scopeRef} />
        </div>

        <div className="flex items-center gap-4">
          <div className="flex items-center gap-1">
            {RANGE_CHOICES.map((d) => (
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

        {link.isLoading && (
          <div className="text-sm text-muted-foreground">Minting link…</div>
        )}
        {link.isError && (
          <div className="text-sm text-destructive">
            Could not create the widget link.
          </div>
        )}
        {link.data && (
          <div className="space-y-4">
            {entries.map((entry) => (
              <WidgetCard
                key={entry.kind}
                entry={entry}
                baseUrl={link.data.widgetBaseUrl}
                days={days}
                theme={theme}
              />
            ))}
          </div>
        )}
      </SheetContent>
    </Sheet>
  );
}
