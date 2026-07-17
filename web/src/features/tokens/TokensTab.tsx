import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { Copy, KeyRound, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { copyToClipboard } from "@/lib/utils";
import type { StoredApiToken } from "@/types/api";

// TokensTab is the page-embedded surface for managing API tokens on
// /app/settings?tab=tokens. It replaces the old top-bar "New API token" button
// plus the TokenListModal/CreateTokenModal pair. Everything is inline — the
// freshly-minted plaintext token appears in a dismissible Card above the
// table rather than a dialog.
//
// Backend caveat: the tokens endpoint only returns still-usable tokens; a
// revoked token disappears from the list entirely. The "Active" pill below
// is therefore a static label per row, not a dynamic status field. If/when
// the backend grows a real status column, feed it into the Badge here.

// Stored id is base64(uuid); decode to hand the raw UUID back to the plugin.
function decodeToken(id: string): string {
  try {
    return atob(id);
  } catch {
    return id;
  }
}

// Tiny inline relative-time formatter — avoids pulling in date-fns just for
// "3 hours ago" strings. Returns "Never" for a null/empty timestamp.
function formatRelative(ts: string | null | undefined): string {
  if (!ts) return "Never";
  const then = new Date(ts).getTime();
  if (!Number.isFinite(then)) return "Never";
  const diff = Date.now() - then;
  if (diff < 0) return "Just now";
  const sec = Math.floor(diff / 1000);
  if (sec < 45) return "Just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} min ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day} day${day === 1 ? "" : "s"} ago`;
  const mo = Math.floor(day / 30);
  if (mo < 12) return `${mo} mo ago`;
  const yr = Math.floor(day / 365);
  return `${yr} yr${yr === 1 ? "" : "s"} ago`;
}

export function TokensTab() {
  const qc = useQueryClient();
  const [freshToken, setFreshToken] = useState<string | null>(null);

  const {
    data: tokens = [],
    isLoading,
  } = useQuery({
    queryKey: qk.tokens(),
    queryFn: () => api.getTokens(),
  });

  const mint = useMutation({
    mutationFn: () => api.createApiToken(),
    onSuccess: (res) => {
      setFreshToken(res.apiToken);
      qc.invalidateQueries({ queryKey: qk.tokens() });
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError
          ? `Failed to create token: ${e.message}`
          : "Failed to create token",
      ),
  });

  const rename = useMutation({
    mutationFn: (v: { tokenId: string; tokenName: string }) =>
      api.renameToken(v),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.tokens() }),
    onError: () => toast.error("Failed to rename the token"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api.deleteToken(id),
    onSuccess: () => {
      toast.success("Token revoked");
      qc.invalidateQueries({ queryKey: qk.tokens() });
    },
    onError: () => toast.error("Failed to revoke the token"),
  });

  // Sort newest-ish first via id (base64 uuids don't sort by time, but this
  // at least gives a stable, deterministic ordering across renders).
  const sorted = [...tokens].sort((a, b) => a.id.localeCompare(b.id));

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold">API tokens</h2>
          <p className="text-sm text-muted-foreground">
            Each token authenticates a Wakatime-compatible plugin against{" "}
            <code className="rounded bg-muted px-1 py-0.5 text-xs">
              /api/v1
            </code>{" "}
            via an{" "}
            <code className="rounded bg-muted px-1 py-0.5 text-xs">
              Authorization: Basic base64(&lt;token&gt;)
            </code>{" "}
            header. See the{" "}
            <Link
              to="/app/settings?tab=plugin"
              className="underline underline-offset-2 hover:text-foreground"
            >
              Plugin setup
            </Link>{" "}
            tab for the exact config snippet.
          </p>
        </div>
        <Button
          onClick={() => mint.mutate()}
          disabled={mint.isPending}
          title="Create a new API token"
        >
          <Plus className="h-4 w-4" />
          Create token
        </Button>
      </div>

      {freshToken !== null && (
        <Card className="border-primary/40">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldCheck className="h-4 w-4 text-primary" />
              New token — copy it now
            </CardTitle>
            <CardDescription>
              Boomtime does not store the raw token. Copy it into your plugin
              config now — after you dismiss this panel it is gone for good.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2 rounded-md border bg-muted p-3">
              <code className="flex-1 select-all break-all text-sm">
                {freshToken}
              </code>
              <Button
                variant="secondary"
                size="icon"
                title="Copy to clipboard"
                onClick={async () => {
                  await copyToClipboard(freshToken);
                  toast.success("Token copied to clipboard");
                }}
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
            <div className="flex justify-end">
              <Button
                variant="secondary"
                onClick={() => setFreshToken(null)}
              >
                Dismiss
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>ID prefix</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading ? (
                <SkeletonRows />
              ) : sorted.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={5}
                    className="py-8 text-center text-muted-foreground"
                  >
                    <KeyRound className="mx-auto mb-2 h-6 w-6 opacity-60" />
                    No tokens yet. Create one to start streaming heartbeats.
                  </TableCell>
                </TableRow>
              ) : (
                sorted.map((t) => (
                  <TokenRow
                    key={t.id}
                    token={t}
                    onRename={(name) =>
                      rename.mutate({ tokenId: t.id, tokenName: name })
                    }
                    onRevoke={() => {
                      const label = t.name || t.id.substring(0, 8);
                      if (
                        window.confirm(
                          `Revoke token "${label}"? Any plugin using it will stop reporting until reconfigured.`,
                        )
                      ) {
                        revoke.mutate(t.id);
                      }
                    }}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

function SkeletonRows() {
  return (
    <>
      {[0, 1, 2].map((i) => (
        <TableRow key={i}>
          {[0, 1, 2, 3, 4].map((j) => (
            <TableCell key={j}>
              <div className="h-4 w-full max-w-[120px] animate-pulse rounded bg-muted" />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}

function TokenRow({
  token,
  onRename,
  onRevoke,
}: {
  token: StoredApiToken;
  onRename: (name: string) => void;
  onRevoke: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(token.name ?? "");

  return (
    <TableRow>
      <TableCell>
        {editing ? (
          <Input
            autoFocus
            maxLength={42}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onBlur={() => {
              setEditing(false);
              if (name && name !== (token.name ?? "")) onRename(name);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") e.currentTarget.blur();
            }}
            className="h-7"
          />
        ) : (
          <button
            className="rounded px-1 hover:bg-accent"
            onClick={() => setEditing(true)}
            title="Click to rename"
          >
            {token.name || <span className="text-muted-foreground">—</span>}
          </button>
        )}
      </TableCell>
      <TableCell className="font-mono text-xs text-muted-foreground">
        {token.id.substring(0, 8)}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatRelative(token.lastUsage)}
      </TableCell>
      <TableCell>
        {/* Static "Active" — the backend only returns non-revoked tokens, so
            every row here is by definition active. Wire this to real state
            when the backend exposes a status field. */}
        <Badge
          variant="secondary"
          className="bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
        >
          Active
        </Badge>
      </TableCell>
      <TableCell className="text-right">
        <div className="flex justify-end gap-1">
          <Button
            variant="secondary"
            size="icon"
            className="h-8 w-8"
            title="Copy raw token"
            onClick={async () => {
              await copyToClipboard(decodeToken(token.id));
              toast.success("Token copied to clipboard");
            }}
          >
            <Copy className="h-4 w-4" />
          </Button>
          <Button
            variant="destructive"
            size="icon"
            className="h-8 w-8"
            title="Revoke token"
            onClick={onRevoke}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}
