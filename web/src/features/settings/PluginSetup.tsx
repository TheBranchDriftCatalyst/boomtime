import { useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { AlertTriangle, Copy, ExternalLink, KeyRound } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { copyToClipboard } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { useAuth } from "@/features/auth/useAuth";

// PluginSetup shows the operator the exact ~/.wakatime.cfg block to point
// any Wakatime-compatible plugin (VSCode, JetBrains, vim, browser plugins,
// custom exporters) at this server. api_url is derived from the browser's
// current origin so it works transparently behind reverse proxies. api_key
// starts as a placeholder; a click on "Generate token" mints a fresh
// per-user API token and drops the raw value straight into the snippet —
// shown once, per the token model, so the user copies it now or generates
// another one later.
//
// This is a one-time-per-plugin flow: mint a token, copy the block, paste
// into ~/.wakatime.cfg, restart the plugin. The Tokens modal (header
// menu) lists / renames / revokes tokens after the fact.
export function PluginSetup() {
  const { username } = useAuth();
  const [token, setToken] = useState<string | null>(null);

  const apiUrl = useMemo(() => {
    if (typeof window === "undefined") return "https://<your-host>/api/v1";
    return `${window.location.origin}/api/v1`;
  }, []);

  const mint = useMutation({
    mutationFn: () => api.createApiToken(),
    onSuccess: (res) => setToken(res.apiToken),
    onError: (e) =>
      toast.error(
        e instanceof ApiError
          ? `Failed to generate token: ${e.message}`
          : "Failed to generate token",
      ),
  });

  const cfg = wakatimeCfg(apiUrl, token);

  async function copy(text: string, label: string) {
    await copyToClipboard(text);
    toast.success(`${label} copied to clipboard`);
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="p-4 pb-0">
          <h2 className="text-lg font-semibold">
            Point a Wakatime plugin at this server
          </h2>
          <p className="text-sm text-muted-foreground">
            Every official Wakatime plugin honours <code>~/.wakatime.cfg</code>{" "}
            (or an <code>api_url</code> setting inside the plugin). Set{" "}
            <code>api_url</code> to your Boomtime host and paste in a personal
            API token — your heartbeats stream here instead of wakatime.com.
          </p>
        </CardHeader>
        <CardContent className="space-y-4 p-4">
          <FieldRow
            label="API URL"
            value={apiUrl}
            onCopy={() => copy(apiUrl, "API URL")}
            hint="Every heartbeat POSTs to this base + /users/current/heartbeats(.bulk)."
          />

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium">API token</label>
              <Button
                size="sm"
                variant={token ? "outline" : "secondary"}
                disabled={mint.isPending}
                onClick={() => mint.mutate()}
              >
                <KeyRound className="mr-1 h-3.5 w-3.5" />
                {mint.isPending
                  ? "Generating…"
                  : token
                    ? "Generate another"
                    : "Generate token"}
              </Button>
            </div>
            {token ? (
              <>
                <div className="flex items-center gap-2 rounded-md border bg-muted/50 p-3">
                  <code className="flex-1 select-all break-all font-mono text-xs">
                    {token}
                  </code>
                  <Button
                    variant="secondary"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => copy(token, "Token")}
                    title="Copy token"
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </Button>
                </div>
                <p className="flex items-start gap-1.5 text-xs text-amber-500">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>
                    This token is displayed only once. Copy it now or generate
                    another one — you can rename / revoke it later from the{" "}
                    <b>Tokens</b> menu (header).
                  </span>
                </p>
              </>
            ) : (
              <p className="rounded border border-dashed border-border/60 bg-muted/20 p-3 text-xs text-muted-foreground">
                Click <b>Generate token</b> to mint a per-user API key for{" "}
                <code>{username || "your account"}</code>. Boomtime shows the
                raw value once, then only the id.
              </p>
            )}
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium">
                <code>~/.wakatime.cfg</code>
              </label>
              <Button
                size="sm"
                variant="ghost"
                className="h-7"
                onClick={() => copy(cfg, "Config snippet")}
              >
                <Copy className="mr-1 h-3.5 w-3.5" />
                Copy
              </Button>
            </div>
            <pre className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
              {cfg}
            </pre>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="p-4 pb-0">
          <h3 className="text-base font-semibold">
            Ingest endpoints (for custom exporters)
          </h3>
          <p className="text-sm text-muted-foreground">
            If you're building your own exporter, POST heartbeats to one of
            these. The bulk endpoint accepts an array of the same shape and is
            what the Wakatime plugins use.
          </p>
        </CardHeader>
        <CardContent className="space-y-2 p-4 font-mono text-xs">
          <EndpointRow method="POST" path="/api/v1/users/current/heartbeats" />
          <EndpointRow method="POST" path="/api/v1/users/current/heartbeats.bulk" />
          <p className="pt-2 font-sans text-xs text-muted-foreground">
            Auth: <code>Authorization: Basic {"<base64(api_key)>"}</code>. The
            key is the raw token string above; Basic-prefixing matches the
            hakatime/wakatime convention.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="p-4 pb-0">
          <h3 className="text-base font-semibold">Where to grab a plugin</h3>
        </CardHeader>
        <CardContent className="p-4">
          <a
            href="https://wakatime.com/plugins"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
          >
            wakatime.com/plugins <ExternalLink className="h-3.5 w-3.5" />
          </a>
          <p className="mt-1 text-xs text-muted-foreground">
            Install the plugin for your editor / browser, then paste the
            snippet above into <code>~/.wakatime.cfg</code>.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

// wakatimeCfg builds the ~/.wakatime.cfg block. When token is null a
// placeholder is shown so the user sees the expected shape before minting.
function wakatimeCfg(apiUrl: string, token: string | null): string {
  return [
    "[settings]",
    `api_url = ${apiUrl}`,
    `api_key = ${token ?? "<generate a token above and it drops in here>"}`,
    "",
  ].join("\n");
}

interface FieldRowProps {
  label: string;
  value: string;
  onCopy: () => void;
  hint?: string;
}

function FieldRow({ label, value, onCopy, hint }: FieldRowProps) {
  return (
    <div className="space-y-1">
      <label className="text-sm font-medium">{label}</label>
      <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-2">
        <code className="flex-1 select-all break-all font-mono text-xs">
          {value}
        </code>
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          onClick={onCopy}
          title={`Copy ${label.toLowerCase()}`}
        >
          <Copy className="h-3.5 w-3.5" />
        </Button>
      </div>
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

interface EndpointRowProps {
  method: string;
  path: string;
}

function EndpointRow({ method, path }: EndpointRowProps) {
  return (
    <div className="flex items-center gap-3 rounded border border-border/60 bg-muted/20 px-3 py-2">
      <span className="rounded bg-primary/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-primary">
        {method}
      </span>
      <code className="flex-1 select-all">{path}</code>
    </div>
  );
}
