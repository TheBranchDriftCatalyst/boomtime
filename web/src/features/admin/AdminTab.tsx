// AdminTab.tsx — Settings > Admin (gaka-myv).
//
// Only rendered when the current user is on the BOOM_ADMIN_USERS allowlist
// (Settings.tsx filters the tab out otherwise; the server also 403s any
// non-admin request, so the tab is a UX aid, not a security boundary).
//
// v1 scope: label-images regeneration. Shows feature status + row count +
// a "Regenerate all" button. Sends the FULL FE catalog snapshot so the Go
// side doesn't have to mirror memecore/kawaii/space-marine expansions.
//
// Future: could grow other operator-only utilities here (rebuild rollups,
// force session invalidation, etc.).
import { useMemo, useState } from "react";
import { RefreshCw } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { LABEL_CATALOG } from "@/features/publicprofile/labels/catalog";
import type { LabelSpec } from "@/features/publicprofile/labels/types";

// Extract the {id, prompt} pairs the backend needs from the FE catalog.
// Labels without an imagePrompt are skipped — no image to generate.
function catalogEntries(): Array<{ id: string; prompt: string }> {
  const out: Array<{ id: string; prompt: string }> = [];
  const seen = new Set<string>();
  for (const s of LABEL_CATALOG as LabelSpec[]) {
    if (!s.imagePrompt || seen.has(s.id)) continue;
    seen.add(s.id);
    out.push({ id: s.id, prompt: s.imagePrompt });
  }
  return out;
}

export function AdminTab() {
  const queryClient = useQueryClient();
  const [lastResult, setLastResult] = useState<
    { generated: number; failed: number; requested: number } | null
  >(null);

  const entries = useMemo(catalogEntries, []);

  const { data, isLoading, isError, error } = useQuery({
    queryKey: qk.adminLabelImages(),
    queryFn: () => api.getAdminLabelImages(),
    staleTime: 10_000,
  });

  const regen = useMutation({
    mutationFn: (opts: { all?: boolean; ids?: string[]; truncate?: boolean }) =>
      api.regenerateLabelImages({
        entries,
        all: opts.all,
        ids: opts.ids,
        truncate: opts.truncate,
      }),
    onSuccess: (res) => {
      setLastResult(res);
      // Bust the info query so the count updates.
      queryClient.invalidateQueries({ queryKey: qk.adminLabelImages() });
    },
  });

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading admin status...</p>;
  }
  if (isError) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/10 p-4 text-sm">
        <p className="font-semibold">Admin access required.</p>
        <p className="mt-2 text-muted-foreground">
          {(error as Error)?.message ?? "You are not on the admin allowlist."}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <section className="rounded-md border border-border bg-card p-4">
        <h2 className="font-mono text-sm font-semibold uppercase tracking-wider">
          Label images
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Generates emblem imagery for every label with an{" "}
          <code className="font-mono text-[10px]">imagePrompt</code> via the
          ComfyUI shim ({data?.shimUrl || "not configured"}). Feature is{" "}
          <strong>{data?.enabled ? "ON" : "OFF"}</strong>.
        </p>

        <dl className="mt-4 grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              Model
            </dt>
            <dd className="mt-1 font-mono">{data?.model ?? "-"}</dd>
          </div>
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              Rows in DB
            </dt>
            <dd className="mt-1 font-mono tabular-nums">{data?.count ?? 0}</dd>
          </div>
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              FE catalog (prompted)
            </dt>
            <dd className="mt-1 font-mono tabular-nums">{entries.length}</dd>
          </div>
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              Baseline labels
            </dt>
            <dd className="mt-1 font-mono tabular-nums">
              {data?.baseline?.length ?? 0}
            </dd>
          </div>
        </dl>

        <div className="mt-6 flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="default"
            onClick={() => regen.mutate({ all: true })}
            disabled={regen.isPending || !data?.enabled}
          >
            <RefreshCw
              size={14}
              className={regen.isPending ? "animate-spin" : ""}
            />
            <span className="ml-2">
              {regen.isPending
                ? "Generating..."
                : `Regenerate all (${entries.length})`}
            </span>
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={() => regen.mutate({ all: true, truncate: true })}
            disabled={regen.isPending || !data?.enabled}
            title="Wipe every row first — guarantees deleted-in-FE labels are also removed"
          >
            Truncate + regenerate all
          </Button>
        </div>

        {!data?.enabled && (
          <p className="mt-4 rounded-sm border border-yellow-500/40 bg-yellow-500/10 p-3 text-xs">
            <strong>Feature disabled.</strong> Set{" "}
            <code className="font-mono">BOOM_FEATURE_LABEL_IMAGES=on</code> AND{" "}
            <code className="font-mono">BOOM_COMFYUI_SHIM_URL=&lt;url&gt;</code>{" "}
            (and optionally{" "}
            <code className="font-mono">BOOM_COMFYUI_MODEL=&lt;pipeline&gt;</code>
            ), then restart boomtime.
          </p>
        )}

        {regen.isError && (
          <p className="mt-4 rounded-sm border border-destructive/40 bg-destructive/10 p-3 text-xs">
            <strong>Regeneration failed.</strong>{" "}
            {(regen.error as Error)?.message ?? "Unknown error."}
          </p>
        )}

        {lastResult && (
          <p className="mt-4 rounded-sm border border-emerald-500/40 bg-emerald-500/10 p-3 text-xs">
            <strong>Last run:</strong> generated {lastResult.generated} /{" "}
            {lastResult.requested} requested (failed: {lastResult.failed}).
          </p>
        )}
      </section>
    </div>
  );
}
