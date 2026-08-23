import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { Github, Link2Off } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { usePublicConfig } from "@shared/lib/usePublicConfig";

// Settings › Profile › Connect GitHub (boom-2ip Phase 1). Mirrors the OIDC
// LinkedIdentitiesCard / Wakatime-key card shape.
//
// GATING: renders NOTHING unless the server advertises
// github_connect_enabled (public config) — which is true only when the gate is
// on AND the OAuth-App creds + state signing key are configured. So the whole
// surface is inert until an operator provisions the secrets.
//
// The connect action is a top-level browser navigation to the backend
// /auth/github/connect (which resolves the owner from the session cookie, signs
// a state, and 302s to GitHub) — NOT an XHR. The callback redirects back here
// with ?github=connected|error|state|exchange|denied, surfaced as a banner.

const GITHUB_BANNER: Record<string, { text: string; ok: boolean }> = {
  connected: { text: "GitHub account connected.", ok: true },
  denied: { text: "GitHub authorization was cancelled.", ok: false },
  state: { text: "GitHub connect failed a security check. Please try again.", ok: false },
  exchange: { text: "Couldn't complete the GitHub connection. Please try again.", ok: false },
  missing_code: { text: "GitHub connect returned no authorization code. Please try again.", ok: false },
  error: { text: "GitHub connection failed. Please try again.", ok: false },
};

export function GithubConnectCard() {
  const qc = useQueryClient();
  const { config } = usePublicConfig();
  const [params, setParams] = useSearchParams();

  const enabled = config.github_connect_enabled;

  const { data, isLoading } = useQuery({
    queryKey: qk.githubConnection(),
    queryFn: () => api.getGithubConnection(),
    staleTime: 30_000,
    enabled, // don't fetch when the feature is off
  });

  const disconnect = useMutation({
    mutationFn: () => api.disconnectGithub(),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.githubConnection() }),
  });

  // Feature off → render nothing (inert surface).
  if (!enabled) return null;

  const banner = GITHUB_BANNER[params.get("github") ?? ""];
  const dismissBanner = () => {
    const next = new URLSearchParams(params);
    next.delete("github");
    setParams(next, { replace: true });
  };

  const connected = data?.connected ?? false;

  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Github className="h-4 w-4 text-primary" />
            Connect GitHub
          </h3>
          <p className="mt-1 text-sm text-muted-foreground">
            Connect your GitHub account so boomtime can surface your GitHub activity
            alongside your coding stats. We store only an encrypted access token — never
            your password.
          </p>
        </div>

        {banner && (
          <div
            className={
              "flex items-center justify-between rounded-md border px-3 py-2 text-sm " +
              (banner.ok
                ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-400"
                : "border-destructive/40 bg-destructive/10 text-destructive")
            }
          >
            <span>{banner.text}</span>
            <button
              type="button"
              onClick={dismissBanner}
              className="text-xs underline-offset-2 hover:underline"
            >
              Dismiss
            </button>
          </div>
        )}

        {isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : connected ? (
          <div className="flex items-center justify-between rounded-md border border-border px-3 py-2.5">
            <div className="min-w-0">
              <div className="text-sm font-medium">
                Connected{data?.login ? ` as @${data.login}` : ""}
              </div>
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
        ) : (
          <Button
            onClick={() => {
              window.location.href = "/auth/github/connect";
            }}
          >
            <Github className="mr-2 h-4 w-4" />
            Connect GitHub
          </Button>
        )}

        {disconnect.isError && (
          <p className="text-xs text-destructive">Couldn&apos;t disconnect — please try again.</p>
        )}
      </CardContent>
    </Card>
  );
}
