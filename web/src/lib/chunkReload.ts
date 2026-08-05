// chunkReload — recover gracefully from stale lazy-chunk imports after a deploy.
//
// When boomtime redeploys, the SPA's content-hashed JS chunks get new names.
// A browser tab that was loaded on the OLD build still references the OLD
// hashes; navigating to a lazily-loaded route then tries to import a chunk that
// no longer exists. The server's SPA fallback returns index.html (HTML) for the
// missing asset path, so the dynamic import fails with a TypeError:
//   "Failed to fetch dynamically imported module: .../assets/Foo-<oldhash>.js"
//
// The correct recovery is a one-shot full reload: it fetches the fresh
// index.html (new hashes) and everything works. We guard against reload loops
// so a genuinely broken build surfaces a real error instead of thrashing.

const GUARD_KEY = "boomtime:chunk-reload-at";
const GUARD_WINDOW_MS = 10_000;

// isChunkLoadError reports whether an error looks like a failed dynamic import
// of a (now-stale) build chunk — across browser wording variants.
export function isChunkLoadError(err: unknown): boolean {
  const msg =
    err instanceof Error
      ? `${err.name}: ${err.message}`
      : typeof err === "string"
        ? err
        : String((err as { message?: unknown })?.message ?? err ?? "");
  return /failed to fetch dynamically imported module|error loading dynamically imported module|importing a module script failed|dynamically imported module|ChunkLoadError|Loading chunk \d+ failed|CSS_?chunk/i.test(
    msg,
  );
}

// reloadOnceForStaleChunk triggers a single full reload to pick up the new
// build. Returns false (without reloading) if we already reloaded within the
// guard window — the caller should then render a real error, because reloading
// again would loop.
export function reloadOnceForStaleChunk(): boolean {
  try {
    const last = Number(sessionStorage.getItem(GUARD_KEY) ?? "0");
    if (Number.isFinite(last) && Date.now() - last < GUARD_WINDOW_MS) {
      return false; // reloaded very recently → don't loop
    }
    sessionStorage.setItem(GUARD_KEY, String(Date.now()));
  } catch {
    // sessionStorage blocked (private mode / partitioned) — reload best-effort.
  }
  // Bypass the bfcache/HTTP cache for index.html so we truly get the new build.
  window.location.reload();
  return true;
}
