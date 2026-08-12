import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, ExternalLink, Link2Off, Upload } from "lucide-react";
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

export function AmazonConnectCard() {
  const qc = useQueryClient();
  const { config } = usePublicConfig();
  const enabled = config.books_enabled;

  const [marketplace, setMarketplace] = useState("us");
  const [session, setSession] = useState<string | null>(null);
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);
  const [redirectUrl, setRedirectUrl] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const { data, isLoading } = useQuery({
    queryKey: AMAZON_CONNECTION_KEY,
    queryFn: () => api.getAmazonConnection(),
    staleTime: 30_000,
    enabled,
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: AMAZON_CONNECTION_KEY });
  const errMsg = (e: unknown) =>
    e instanceof ApiError ? e.message : "Something went wrong — please try again.";

  const start = useMutation({
    mutationFn: () => api.amazonConnectStart({ marketplace }),
    onSuccess: (res) => {
      setSession(res.session);
      setAuthorizeUrl(res.authorizeUrl);
      setRedirectUrl("");
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

  if (!enabled) return null;

  const connected = data?.connected ?? false;
  const connecting = session !== null;

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
          <div className="flex items-center justify-between rounded-md border border-border px-3 py-2.5">
            <div className="min-w-0">
              <div className="text-sm font-medium">Amazon connected</div>
              {data?.status && (
                <div className="truncate text-xs text-muted-foreground">
                  Device status: {data.status}
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
        ) : connecting ? (
          <div className="space-y-3 rounded-md border border-border p-3">
            <p className="text-sm">
              <span className="font-medium">Step 2 —</span> a new tab opened to Amazon. Sign in
              there, and once you land on a blank <code className="text-xs">amazon…/ap/maplanding</code>{" "}
              page, copy that page&apos;s full URL from the address bar and paste it below.
            </p>
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
