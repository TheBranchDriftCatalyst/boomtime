import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { BookOpen, Bookmark, ExternalLink, Link2Off, RefreshCw, Library, Upload } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@/lib/api";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { ApiError } from "@/lib/api";

// Settings › Connect Amazon (catalyst-books + catalyst-audiobooks). ONE Amazon
// device link feeds BOTH Kindle + Audible. Renders nothing unless the server
// advertises books_enabled (BOOM_FEATURE_BOOKS).
//
// Amazon has no third-party OAuth app for reader data, so this is a
// paste-the-URL flow: we build the Amazon /ap/signin URL, the user logs in and
// lands on an .../ap/maplanding URL, and pastes that back so we can exchange the
// authorization_code for a device credential (stored encrypted, never returned).

const MARKETPLACES: Array<{ id: string; label: string }> = [
  { id: "us", label: "amazon.com (US)" },
  { id: "uk", label: "amazon.co.uk (UK)" },
  { id: "de", label: "amazon.de (DE)" },
  { id: "ca", label: "amazon.ca (CA)" },
  { id: "au", label: "amazon.com.au (AU)" },
  { id: "fr", label: "amazon.fr (FR)" },
  { id: "it", label: "amazon.it (IT)" },
  { id: "es", label: "amazon.es (ES)" },
  { id: "in", label: "amazon.in (IN)" },
  { id: "jp", label: "amazon.co.jp (JP)" },
  { id: "br", label: "amazon.com.br (BR)" },
];

const AMAZON_CONNECTION_KEY = ["amazon-connection"] as const;
const BOOKS_ITEMS_KEY = ["books-items", "audible"] as const;

// Survives the full-page navigation the bookmarklet performs from amazon.com
// back to /app/settings?amazonCaptured=... — React state is gone by then, so the
// connect session token is stashed here and read back on return.
const AMAZON_SESSION_STORAGE_KEY = "boomtime.amazon.session";

export function AmazonConnectCard() {
  const qc = useQueryClient();
  const { config } = usePublicConfig();
  const enabled = config.books_enabled;
  const [params, setParams] = useSearchParams();

  const [marketplace, setMarketplace] = useState("us");
  const [session, setSession] = useState<string | null>(null);
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);
  const [redirectUrl, setRedirectUrl] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  // Last ?amazonCaptured value we acted on, so the auto-complete effect fires
  // at most once per captured URL even as the query invalidates and re-renders.
  const handledCaptureRef = useRef<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: AMAZON_CONNECTION_KEY,
    queryFn: () => api.getAmazonConnection(),
    staleTime: 30_000,
    enabled,
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: AMAZON_CONNECTION_KEY });
  const errMsg = (e: unknown) =>
    e instanceof ApiError ? e.message : "Something went wrong — please try again.";

  const clearStoredSession = () => localStorage.removeItem(AMAZON_SESSION_STORAGE_KEY);

  const start = useMutation({
    mutationFn: () => api.amazonConnectStart({ marketplace }),
    onSuccess: (res) => {
      setSession(res.session);
      setAuthorizeUrl(res.authorizeUrl);
      setRedirectUrl("");
      // Persist so the bookmarklet round-trip (which navigates the tab away and
      // back) can still find the session on return.
      localStorage.setItem(AMAZON_SESSION_STORAGE_KEY, res.session);
      window.open(res.authorizeUrl, "_blank", "noopener");
    },
  });

  const complete = useMutation({
    mutationFn: () =>
      api.amazonConnectComplete({ session: session ?? "", redirectUrl: redirectUrl.trim() }),
    onSuccess: () => {
      setSession(null);
      setAuthorizeUrl(null);
      setRedirectUrl("");
      clearStoredSession();
      invalidate();
    },
  });

  // Auto-complete when the bookmarklet returns us to /app/settings?amazonCaptured=…
  // The captured URL carries the Amazon authorization_code; we pair it with the
  // session token stashed in localStorage at start and exchange it server-side.
  const captureComplete = useMutation({
    mutationFn: (args: { session: string; redirectUrl: string }) =>
      api.amazonConnectComplete(args),
    onSuccess: () => {
      setSession(null);
      setAuthorizeUrl(null);
      setRedirectUrl("");
      clearStoredSession();
      const next = new URLSearchParams(params);
      next.delete("amazonCaptured");
      setParams(next, { replace: true });
      invalidate();
    },
  });

  const importFile = useMutation({
    mutationFn: (json: unknown) => api.amazonImportAuth(json),
    onSuccess: () => {
      setImportOpen(false);
      invalidate();
    },
  });

  const disconnect = useMutation({
    mutationFn: () => api.disconnectAmazon(),
    onSuccess: invalidate,
  });

  // Synced-item count for the connected state (only queried once Amazon is
  // linked). Refreshed after a sync/backfill so the number reflects the run.
  const connectedNow = data?.connected ?? false;
  const { data: itemsData } = useQuery({
    queryKey: BOOKS_ITEMS_KEY,
    queryFn: () => api.getBooksItems("audible"),
    enabled: enabled && connectedNow,
    staleTime: 30_000,
  });
  const syncedCount = itemsData?.items.length ?? 0;
  const invalidateItems = () => qc.invalidateQueries({ queryKey: BOOKS_ITEMS_KEY });

  const syncNow = useMutation({
    mutationFn: () => api.syncAudible(),
    onSuccess: invalidateItems,
  });
  const backfill = useMutation({
    mutationFn: () => api.backfillAudible(),
    // The backfill runs on the worker; give it a beat, then refresh the count.
    onSuccess: () => setTimeout(invalidateItems, 2000),
  });

  const captured = params.get("amazonCaptured");
  useEffect(() => {
    if (!captured) return;
    // Fire once per distinct captured URL — the mutation + invalidate would
    // otherwise re-trigger us until the param is finally removed.
    if (handledCaptureRef.current === captured) return;
    handledCaptureRef.current = captured;

    const stored = localStorage.getItem(AMAZON_SESSION_STORAGE_KEY);
    if (!stored) {
      // No session to pair with (e.g. a stale bookmarklet click) — just drop
      // the param so we fall back to the normal connect UI.
      const next = new URLSearchParams(params);
      next.delete("amazonCaptured");
      setParams(next, { replace: true });
      return;
    }
    captureComplete.mutate({ session: stored, redirectUrl: decodeURIComponent(captured) });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [captured]);

  if (!enabled) return null;

  const connected = data?.connected ?? false;
  const connecting = session !== null;
  // Returning from the bookmarklet: the param is present (or we're mid-exchange).
  const returning = captured != null || captureComplete.isPending;

  // One-click capture bookmarklet. Runs IN the amazon.com page context (where we
  // can read window.location.href) and navigates the tab back to boomtime with
  // the maplanding URL. The app origin is baked in at render time — this card
  // always runs on boomtime, so window.location.origin is the right target. Built
  // as a string (never a literal javascript: in JSX) so lint/React don't strip it.
  const captureReturn = JSON.stringify(
    `${window.location.origin}/app/settings?tab=connections&amazonCaptured=`,
  );
  const bookmarkletHref =
    "javascript:(function(){var u=window.location.href;" +
    "if(u.indexOf('openid.oa2.authorization_code')<0){" +
    "alert('Not an Amazon sign-in redirect \\u2014 complete the Amazon login first, then click this on the maplanding page.');return;}" +
    `window.location.href=${captureReturn}+encodeURIComponent(u);})();`;

  const onFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    f.text().then((text) => {
      try {
        importFile.mutate(JSON.parse(text));
      } catch {
        importFile.reset();
        // surface a parse error through the same channel
        importFile.mutate(text as unknown);
      }
    });
    if (fileRef.current) fileRef.current.value = "";
  };

  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <BookOpen className="h-4 w-4 text-primary" />
            Connect Amazon
          </h3>
          <p className="mt-1 text-sm text-muted-foreground">
            Link your Amazon account once to track both <strong>Kindle</strong> reading and{" "}
            <strong>Audible</strong> listening. We register a device and store only an encrypted
            credential — never your Amazon password.
          </p>
        </div>

        {isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : connected ? (
          <div className="space-y-3">
            <div className="flex items-center justify-between rounded-md border border-border px-3 py-2.5">
              <div className="min-w-0">
                <div className="text-sm font-medium">Amazon connected</div>
                <div className="truncate text-xs text-muted-foreground">
                  {syncedCount > 0 ? (
                    <>
                      <Library className="mr-1 inline h-3 w-3" />
                      {syncedCount} Audible {syncedCount === 1 ? "title" : "titles"} synced
                    </>
                  ) : (
                    "No Audible titles synced yet — run a backfill to import your library."
                  )}
                  {data?.status && <> · device: {data.status}</>}
                </div>
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

            <div className="flex flex-wrap items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={backfill.isPending}
                onClick={() => backfill.mutate()}
              >
                <Library className="mr-1.5 h-3.5 w-3.5" />
                {backfill.isPending ? "Starting…" : "Backfill all-time"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                disabled={syncNow.isPending}
                onClick={() => syncNow.mutate()}
              >
                <RefreshCw
                  className={`mr-1.5 h-3.5 w-3.5${syncNow.isPending ? " animate-spin" : ""}`}
                />
                {syncNow.isPending ? "Syncing…" : "Sync now"}
              </Button>
            </div>
            {backfill.isSuccess && (
              <p className="text-xs text-muted-foreground">
                All-time backfill queued — your full Audible library, finish dates, and listening
                history will import in the background.
              </p>
            )}
            {syncNow.isSuccess && (
              <p className="text-xs text-muted-foreground">
                Synced {syncNow.data?.synced ?? 0} Audible {(syncNow.data?.synced ?? 0) === 1 ? "title" : "titles"}.
              </p>
            )}
            {(backfill.isError || syncNow.isError) && (
              <p className="text-xs text-destructive">
                {errMsg(backfill.error ?? syncNow.error)}
              </p>
            )}
          </div>
        ) : returning ? (
          <div className="space-y-2 rounded-md border border-border p-3">
            <div className="text-sm font-medium">Connecting…</div>
            <p className="text-sm text-muted-foreground">
              Finishing the Amazon link from the captured sign-in page.
            </p>
            {captureComplete.isError && (
              <p className="text-xs text-destructive">{errMsg(captureComplete.error)}</p>
            )}
          </div>
        ) : connecting ? (
          <div className="space-y-3 rounded-md border border-border p-3">
            <p className="text-sm">
              <span className="font-medium">Step 2 —</span> a new tab opened to Amazon. Sign in
              there, and once you land on a blank <code className="text-xs">amazon…/ap/maplanding</code>{" "}
              page, use the one-click bookmarklet below (or copy the URL and paste it as a fallback).
            </p>

            <div className="rounded-md border border-dashed border-border p-3">
              <div className="mb-2 text-xs font-medium">One-click capture (recommended)</div>
              <a
                href={bookmarkletHref}
                draggable
                onClick={(e) => e.preventDefault()}
                className="inline-flex cursor-grab items-center gap-1.5 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-primary hover:underline"
              >
                <Bookmark className="h-3.5 w-3.5" />
                Capture Amazon URL
              </a>
              <p className="mt-2 text-xs text-muted-foreground">
                Drag this to your bookmarks bar, then click it on the Amazon page after you log in —
                no copy-paste needed.
              </p>
            </div>

            <div className="text-xs text-muted-foreground">Or paste the URL manually:</div>
            {authorizeUrl && (
              <a
                href={authorizeUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
              >
                <ExternalLink className="h-3 w-3" />
                Re-open the Amazon sign-in tab
              </a>
            )}
            <textarea
              value={redirectUrl}
              onChange={(e) => setRedirectUrl(e.target.value)}
              rows={3}
              placeholder="https://www.amazon.com/ap/maplanding?...&openid.oa2.authorization_code=..."
              className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono"
            />
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                disabled={complete.isPending || redirectUrl.trim() === ""}
                onClick={() => complete.mutate()}
              >
                {complete.isPending ? "Connecting…" : "Finish connecting"}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setSession(null);
                  setAuthorizeUrl(null);
                  setRedirectUrl("");
                  clearStoredSession();
                }}
              >
                Cancel
              </Button>
            </div>
            {complete.isError && (
              <p className="text-xs text-destructive">{errMsg(complete.error)}</p>
            )}
          </div>
        ) : (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
              <label className="text-xs text-muted-foreground">Marketplace</label>
              <select
                value={marketplace}
                onChange={(e) => setMarketplace(e.target.value)}
                className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
              >
                {MARKETPLACES.map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.label}
                  </option>
                ))}
              </select>
              <Button size="sm" disabled={start.isPending} onClick={() => start.mutate()}>
                <BookOpen className="mr-2 h-4 w-4" />
                {start.isPending ? "Starting…" : "Connect Amazon"}
              </Button>
            </div>
            {start.isError && <p className="text-xs text-destructive">{errMsg(start.error)}</p>}

            <button
              type="button"
              onClick={() => setImportOpen((v) => !v)}
              className="text-xs text-muted-foreground underline-offset-2 hover:underline"
            >
              {importOpen ? "Hide" : "Advanced:"} import an existing <code>.audible</code> auth file
            </button>
            {importOpen && (
              <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">
                <p className="mb-2">
                  If you already ran <code>audible quickstart</code>, upload the resulting{" "}
                  <code>.audible</code> JSON file.
                </p>
                <input
                  ref={fileRef}
                  type="file"
                  accept=".audible,.json,application/json"
                  onChange={onFile}
                  className="hidden"
                />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={importFile.isPending}
                  onClick={() => fileRef.current?.click()}
                >
                  <Upload className="mr-2 h-3.5 w-3.5" />
                  {importFile.isPending ? "Importing…" : "Choose file"}
                </Button>
                {importFile.isError && (
                  <p className="mt-2 text-destructive">{errMsg(importFile.error)}</p>
                )}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
