import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { Link2, Link2Off, ShieldCheck } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";

// Settings › Account › Linked identities (boom-b5n.4). Lists the caller's
// linked external identities (Authentik/OIDC) and lets them link a new one or
// unlink an existing one. "Link Authentik" navigates to the backend
// /auth/link/oidc, which binds the resolved identity to the CURRENT account
// (works while provider=local, so you link before flipping to oidc). The
// callback redirects back here with ?link=success|error|conflict.

const LINK_BANNER: Record<string, { text: string; ok: boolean }> = {
  success: { text: "Authentik account linked.", ok: true },
  conflict: { text: "That Authentik identity is already linked to another account.", ok: false },
  error: { text: "Linking failed. Please try again.", ok: false },
};

export function LinkedIdentitiesCard() {
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const { data, isLoading } = useQuery({
    queryKey: qk.identities(),
    queryFn: () => api.getIdentities(),
    staleTime: 30_000,
  });

  const unlink = useMutation({
    mutationFn: (provider: string) => api.unlinkIdentity(provider),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.identities() }),
  });

  // Don't render the card at all when OIDC isn't configured — nothing to link.
  if (!isLoading && data && !data.oidcAvailable) return null;

  const banner = LINK_BANNER[params.get("link") ?? ""];
  const dismissBanner = () => {
    const next = new URLSearchParams(params);
    next.delete("link");
    setParams(next, { replace: true });
  };

  const identities = data?.identities ?? [];
  const linkedProviders = new Set(identities.map((i) => i.provider));

  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <ShieldCheck className="h-4 w-4 text-primary" />
            Linked sign-in identities
          </h3>
          <p className="mt-1 text-sm text-muted-foreground">
            Link your account to Authentik so you can sign in through it. Do this before
            an admin switches this server to OIDC-only login.
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
        ) : identities.length === 0 ? (
          <p className="text-sm text-muted-foreground">No linked identities yet.</p>
        ) : (
          <ul className="divide-y divide-border rounded-md border border-border">
            {identities.map((id) => (
              <li key={id.provider} className="flex items-center justify-between px-3 py-2.5">
                <div className="min-w-0">
                  <div className="text-sm font-medium capitalize">{id.provider}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {id.email || id.subPrefix + "…"} · linked {id.linkedAt.slice(0, 10)}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={unlink.isPending}
                  onClick={() => unlink.mutate(id.provider)}
                  className="text-destructive hover:text-destructive"
                >
                  <Link2Off className="mr-1.5 h-3.5 w-3.5" />
                  Unlink
                </Button>
              </li>
            ))}
          </ul>
        )}

        {unlink.isError && (
          <p className="text-xs text-destructive">
            Couldn&apos;t unlink — you may need to set a password first (can&apos;t remove your
            only sign-in method).
          </p>
        )}

        {!linkedProviders.has("authentik") && (
          <Button
            onClick={() => {
              window.location.href = "/auth/link/oidc";
            }}
          >
            <Link2 className="mr-2 h-4 w-4" />
            Link Authentik
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
