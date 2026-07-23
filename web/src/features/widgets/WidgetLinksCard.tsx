import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { formatDistanceToNowStrict } from "date-fns";
import { Globe, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { WidgetLinkRow, WidgetLinksPayload } from "@/types/api";

/** Row label — prefer the resolved scopeName, fall back to the ref (which is
 * the project name for project scope, or a space id for space scope). */
function scopeLabel(row: WidgetLinkRow): string {
  const name = row.scopeName || row.scopeRef;
  switch (row.scopeType) {
    case "user":
      return "Account-wide";
    case "project":
      return `Project: ${name}`;
    case "space":
      return name ? `Space: ${name}` : `Space #${row.scopeRef}`;
  }
}

// Settings section: every minted widget link with a Roll action + usage
// badge. Delete was removed — rolling covers the "invalidate a leaked URL"
// case without leaving a scope link-less. The badge shows "last requested
// Nm ago" for used links; clicking it opens a popover with the unique
// origin set (Referer URLs / "direct" for headerless fetches like GitHub
// camo). Origins are tracked backend-side up to 20 most-recent unique
// values (see RecordWidgetLinkHit).
export function WidgetLinksCard() {
  const qc = useQueryClient();
  const links = useQuery({
    queryKey: qk.widgetLinks(),
    queryFn: () => api.listWidgetLinks(),
  });

  const roll = useMutation({
    mutationFn: (linkId: string) => api.rollWidgetLink(linkId),
    onSuccess: (fresh, oldId) => {
      // Optimistic swap — the list reflects the new id INSTANTLY, no
      // refetch flicker. lastUsedAt + origins reset (the old id is gone).
      qc.setQueryData<WidgetLinksPayload>(qk.widgetLinks(), (old) =>
        old
          ? {
              links: old.links.map((l) =>
                l.linkId === oldId
                  ? {
                      ...l,
                      linkId: fresh.linkId,
                      lastUsedAt: undefined,
                      origins: [],
                    }
                  : l,
              ),
            }
          : old,
      );
      qc.invalidateQueries({ queryKey: ["widget-link"] });
      toast.success(
        "Rolled — old link now 404s. Update any README embeds to the new URL.",
      );
    },
    onError: () => toast.error("Failed to roll the widget link"),
  });

  const rows = links.data?.links ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Widget links</CardTitle>
        <p className="text-sm text-muted-foreground">
          Public links minted for embeddable widgets.{" "}
          <span className="font-medium text-foreground">Roll</span> mints a new
          uuid for the same scope — the old URL immediately 404s, the new URL
          serves. Click the last-requested badge to see the origins.
        </p>
      </CardHeader>
      <CardContent>
        {links.isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No widget links yet — open the Widgets panel on any dashboard to
            create one.
          </p>
        ) : (
          <ul className="space-y-2">
            {rows.map((row) => (
              <li
                key={row.linkId}
                className="flex items-center justify-between gap-2 rounded border border-border px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">
                      {scopeLabel(row)}
                    </span>
                    <UsageBadge row={row} />
                  </div>
                  <code className="block truncate text-xs text-muted-foreground">
                    {row.linkId}
                  </code>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 shrink-0"
                  aria-label={`Roll widget link for ${scopeLabel(row)}`}
                  title="Roll — mint a new uuid, invalidate the old one"
                  disabled={roll.isPending}
                  onClick={() => roll.mutate(row.linkId)}
                >
                  <RefreshCw className="h-4 w-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

/** Small badge showing "Last: 2m ago" — click opens a popover with the
 * unique origin set. Renders "Unused" when nothing has fetched the URL yet. */
function UsageBadge({ row }: { row: WidgetLinkRow }) {
  const origins = row.origins ?? [];
  if (!row.lastUsedAt) {
    return (
      <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
        Unused
      </span>
    );
  }
  const rel = formatDistanceToNowStrict(new Date(row.lastUsedAt), {
    addSuffix: true,
  });
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary transition-colors hover:bg-primary/25"
          aria-label={`Show origins for ${row.linkId}`}
        >
          <Globe className="h-2.5 w-2.5" />
          {rel}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-3">
        <div className="mb-2 text-xs font-semibold">
          Requests ({origins.length} unique {origins.length === 1 ? "origin" : "origins"})
        </div>
        {origins.length === 0 ? (
          <p className="text-xs text-muted-foreground">No origins recorded.</p>
        ) : (
          <ul className="space-y-1.5">
            {origins.map((o) => (
              <li
                key={o.origin}
                className="flex items-center justify-between gap-2 text-xs"
              >
                <span className="min-w-0 truncate" title={o.origin}>
                  {o.origin === "direct" ? (
                    <em className="text-muted-foreground">direct / no Referer</em>
                  ) : (
                    o.origin
                  )}
                </span>
                <span className="shrink-0 text-muted-foreground">
                  {o.count}× ·{" "}
                  {formatDistanceToNowStrict(new Date(o.lastSeen), {
                    addSuffix: true,
                  })}
                </span>
              </li>
            ))}
          </ul>
        )}
      </PopoverContent>
    </Popover>
  );
}
