import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { WidgetLinkRow } from "@/types/api";

function scopeLabel(row: WidgetLinkRow): string {
  switch (row.scopeType) {
    case "user":
      return "Account-wide";
    case "project":
      return `Project: ${row.scopeRef}`;
    case "space":
      return `Space #${row.scopeRef}`;
  }
}

// Settings section: every minted widget link with a revoke button. Revoking
// kills ALL embeds using that link (they 404) — the copy warns about that.
export function WidgetLinksCard() {
  const qc = useQueryClient();
  const links = useQuery({
    queryKey: qk.widgetLinks(),
    queryFn: () => api.listWidgetLinks(),
  });
  const revoke = useMutation({
    mutationFn: (linkId: string) => api.deleteWidgetLink(linkId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.widgetLinks() });
      // Any cached mint results are now stale too.
      qc.invalidateQueries({ queryKey: ["widget-link"] });
      toast.success("Widget link revoked — its embeds will stop rendering");
    },
    onError: () => toast.error("Failed to revoke the widget link"),
  });

  const rows = links.data?.links ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Widget links</CardTitle>
        <p className="text-sm text-muted-foreground">
          Public links minted for embeddable widgets. Revoking a link breaks
          every README/blog embed that uses it.
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
                <div className="min-w-0">
                  <div className="text-sm font-medium">{scopeLabel(row)}</div>
                  <code className="block truncate text-xs text-muted-foreground">
                    {row.linkId}
                  </code>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8 shrink-0 text-destructive"
                  aria-label={`Revoke widget link for ${scopeLabel(row)}`}
                  disabled={revoke.isPending}
                  onClick={() => revoke.mutate(row.linkId)}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
