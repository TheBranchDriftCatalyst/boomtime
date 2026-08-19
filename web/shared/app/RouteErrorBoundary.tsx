import { useEffect, useState } from "react";
import { useRouteError, useNavigate, isRouteErrorResponse } from "react-router";
import { isChunkLoadError, reloadOnceForStaleChunk } from "@shared/lib/chunkReload";

// RouteErrorBoundary — the router-level errorElement. Replaces react-router's
// default "Unexpected Application Error! / Hey developer 👋" dev screen.
//
// Its most important job: recover from a STALE lazy-chunk import after a deploy
// (see lib/chunkReload). When a still-open tab navigates to a route whose chunk
// hash changed, the import fails; we reload ONCE to fetch the new build. Only if
// that recovery already ran (guard window) — i.e. it's not merely stale — do we
// surface a real error screen.
export function RouteErrorBoundary() {
  const error = useRouteError();
  const navigate = useNavigate();
  const stale = isChunkLoadError(error) || isChunkLoadError((error as { error?: unknown })?.error);
  const [recovering, setRecovering] = useState(stale);

  useEffect(() => {
    if (!stale) return;
    // Reload to the fresh build; if we already reloaded very recently, fall
    // through to the error UI instead of looping.
    if (!reloadOnceForStaleChunk()) setRecovering(false);
  }, [stale]);

  if (recovering) {
    return <FullScreen>
      <div className="h-8 w-8 animate-spin rounded-full border-2 border-primary/30 border-t-primary" />
      <p className="mt-4 text-sm text-muted-foreground">Updating to the latest version…</p>
    </FullScreen>;
  }

  const status = isRouteErrorResponse(error) ? error.status : undefined;
  const detail =
    error instanceof Error ? error.message : isRouteErrorResponse(error) ? error.statusText : String(error ?? "");

  return (
    <FullScreen>
      <div className="max-w-md text-center">
        <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-destructive/10 text-2xl">
          ⚠️
        </div>
        <h1 className="text-lg font-semibold text-foreground">
          {status === 404 ? "Page not found" : "Something went wrong"}
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {status === 404
            ? "That page doesn’t exist. It may have moved."
            : "An unexpected error occurred. Reloading usually clears it."}
        </p>
        <div className="mt-6 flex items-center justify-center gap-3">
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
          >
            Reload
          </button>
          <button
            type="button"
            onClick={() => navigate("/app", { replace: true })}
            className="rounded-md border border-border px-4 py-2 text-sm font-medium text-foreground transition-colors hover:bg-accent"
          >
            Go home
          </button>
        </div>
        {import.meta.env.DEV && detail ? (
          <pre className="mt-6 max-h-40 overflow-auto rounded-md border border-border bg-muted/40 p-3 text-left text-xs text-muted-foreground">
            {detail}
          </pre>
        ) : null}
      </div>
    </FullScreen>
  );
}

function FullScreen({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen w-full flex-col items-center justify-center bg-background p-6">
      {children}
    </div>
  );
}
