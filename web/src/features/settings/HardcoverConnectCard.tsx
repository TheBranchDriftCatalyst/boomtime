import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { DownloadCloud, ExternalLink, Library, Link2Off, Search } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api, ApiError } from "@/lib/api";
import { usePublicConfig } from "@/lib/usePublicConfig";

// Settings › Connections › Connect Hardcover (catalyst-books PUSH target).
// Hardcover is where boomtime mirrors your reading state OUT. Auth is a
// user-pasted bearer token from Hardcover account settings that expires yearly
// + resets every Jan 1 — so a re-paste is a routine event, and an "invalid"
// status is a prompt to re-paste, not a failure.
//
// Renders NOTHING unless the server advertises books_enabled (BOOM_FEATURE_BOOKS)
// — the same gate as the Amazon card, so the whole surface is inert per
// deployment. The token is validated server-side (me{} query) before it is
// stored, and it NEVER leaves the server after.

const HARDCOVER_CONNECTION_KEY = ["hardcover-connection"] as const;
const HARDCOVER_TOKEN_URL = "https://hardcover.app/account/api";

export function HardcoverConnectCard() {
  const qc = useQueryClient();
  const { config } = usePublicConfig();
  const enabled = config.books_enabled;

  const [token, setToken] = useState("");

  const { data, isLoading } = useQuery({
    queryKey: HARDCOVER_CONNECTION_KEY,
    queryFn: () => api.getHardcoverConnection(),
    staleTime: 30_000,
    enabled,
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: HARDCOVER_CONNECTION_KEY });
  const errMsg = (e: unknown) =>
    e instanceof ApiError ? e.message : "Something went wrong — please try again.";

  const connect = useMutation({
    mutationFn: () => api.connectHardcover({ token: token.trim() }),
    onSuccess: () => {
      setToken("");
      invalidate();
    },
  });

  const disconnect = useMutation({
    mutationFn: () => api.disconnectHardcover(),
    onSuccess: invalidate,
  });

  // On-demand pipeline steps. Both READ-ONLY / safe — they enqueue a worker job
  // (returns a jobId) and never write to the Hardcover shelf. Outcome via toast.
  const match = useMutation({
    mutationFn: () => api.matchHardcover(),
    onSuccess: (res) => toast.success(`Hardcover match started (job #${res.jobId})`),
    onError: (e) => toast.error(errMsg(e)),
  });
  const pull = useMutation({
    mutationFn: () => api.pullHardcover(),
    onSuccess: (res) => toast.success(`Hardcover pull started (job #${res.jobId})`),
    onError: (e) => toast.error(errMsg(e)),
  });

  if (!enabled) return null;

  const connected = data?.connected ?? false;
  const invalid = connected && data?.status === "invalid";

  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Library className="h-4 w-4 text-primary" />
            Connect Hardcover
          </h3>
          <p className="mt-1 text-sm text-muted-foreground">
            Push your Kindle + Audible reading state out to{" "}
            <a
              href="https://hardcover.app"
              target="_blank"
              rel="noopener noreferrer"
              className="text-primary hover:underline"
            >
              Hardcover
            </a>
            . Paste your API bearer token below — we store only an encrypted copy and never your
            Hardcover password.
          </p>
        </div>

        {isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : connected ? (
          <div className="space-y-3">
            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2.5">
              <div className="min-w-0">
                <div className="text-sm font-medium">Hardcover connected</div>
                {data?.status && (
                  <div className="truncate text-xs text-muted-foreground">
                    Token status: {data.status}
                  </div>
                )}
              </div>
              <Button
                variant="ghost"
                size="sm"
                disabled={disconnect.isPending}
                onClick={() => disconnect.mutate()}
                className="text-destructive hover:text-destructive"
              >
                <Link2Off className="mr-1.5 h-3.5 w-3.5" />
                Disconnect
              </Button>
            </div>

            <div className="space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={match.isPending}
                  onClick={() => match.mutate()}
                >
                  <Search className="mr-1.5 h-3.5 w-3.5" />
                  {match.isPending ? "Starting…" : "Match books"}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={pull.isPending}
                  onClick={() => pull.mutate()}
                >
                  <DownloadCloud className="mr-1.5 h-3.5 w-3.5" />
                  {pull.isPending ? "Starting…" : "Pull from Hardcover"}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                Both are read-only and safe — they match your library against Hardcover and pull
                your reading state in. Neither writes to your Hardcover shelf (outbound writes stay
                dry-run-gated).
              </p>
            </div>

            {invalid && (
              <div className="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
                <p className="text-xs text-amber-500">
                  Your Hardcover token was rejected — it may have expired (tokens reset every Jan 1).
                  Paste a fresh one to reconnect.
                </p>
                <TokenField
                  token={token}
                  setToken={setToken}
                  pending={connect.isPending}
                  onConnect={() => connect.mutate()}
                />
                {connect.isError && (
                  <p className="text-xs text-destructive">{errMsg(connect.error)}</p>
                )}
              </div>
            )}
          </div>
        ) : (
          <div className="space-y-2">
            <TokenField
              token={token}
              setToken={setToken}
              pending={connect.isPending}
              onConnect={() => connect.mutate()}
            />
            <a
              href={HARDCOVER_TOKEN_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
            >
              <ExternalLink className="h-3 w-3" />
              Where do I find my Hardcover API token?
            </a>
            {connect.isError && <p className="text-xs text-destructive">{errMsg(connect.error)}</p>}
          </div>
        )}

        {disconnect.isError && (
          <p className="text-xs text-destructive">Couldn&apos;t disconnect — please try again.</p>
        )}
      </CardContent>
    </Card>
  );
}

function TokenField({
  token,
  setToken,
  pending,
  onConnect,
}: {
  token: string;
  setToken: (v: string) => void;
  pending: boolean;
  onConnect: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row">
      <input
        type="password"
        value={token}
        onChange={(e) => setToken(e.target.value)}
        placeholder="Paste your Hardcover API token"
        autoComplete="off"
        className="flex-1 rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
      />
      <Button size="sm" disabled={pending || token.trim() === ""} onClick={onConnect}>
        <Library className="mr-2 h-4 w-4" />
        {pending ? "Connecting…" : "Connect"}
      </Button>
    </div>
  );
}
