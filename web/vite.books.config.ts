// Standalone catalyst-books build (gaka-zp2s). Reuses the host vite config
// verbatim (same plugins, resolve aliases, and manualChunks vendor-splitting)
// but swaps the HTML entry to index.books.html (which loads src/books-main.tsx,
// composing ONLY the core + books domains) and emits to dist-books/.
//
// Because books-main.tsx never imports registerBoomtimeDomain, the boomtime
// code-domain page modules (Projects / Leaderboards / Heartbeats / Wellness /
// SpaceView / Goals / Import / catalog / boomtime admin tabs) are unreachable
// from this entry graph and are dropped — the standalone image ships only the
// shared shell + the books surface.
import { defineConfig, type Plugin, type UserConfig } from "vite";
import path from "node:path";
import { promises as fs } from "node:fs";

import base from "./vite.config";

// Vite emits the entry HTML at its path relative to the project root, i.e.
// dist-books/index.books.html. The Go server (internal/books/web) embeds and
// serves index.html, so rename the emitted shell after the bundle is written.
function renameBooksIndexHtml(): Plugin {
  const outDir = path.resolve(__dirname, "dist-books");
  return {
    name: "catalyst-books-rename-index",
    async closeBundle() {
      const from = path.join(outDir, "index.books.html");
      const to = path.join(outDir, "index.html");
      try {
        await fs.rename(from, to);
      } catch {
        /* nothing emitted (e.g. --watch teardown) — ignore */
      }
    },
  };
}

const baseConfig = base as UserConfig;

export default defineConfig({
  ...baseConfig,
  plugins: [...(baseConfig.plugins ?? []), renameBooksIndexHtml()],
  // Standalone flags consumed by src/features/auth/useAuth.tsx (single-owner, no
  // auth) — the FE counterpart of the backend's auth.SetStandaloneOwner. Only the
  // books build sets these; the host build never does.
  define: {
    ...(baseConfig.define ?? {}),
    "import.meta.env.VITE_BOOKS_STANDALONE": JSON.stringify("true"),
    "import.meta.env.VITE_BOOKS_OWNER": JSON.stringify("owner"),
  },
  build: {
    ...baseConfig.build,
    outDir: "dist-books",
    emptyOutDir: true,
    // This Vite is rolldown-based; the base config drives chunking through
    // `build.rolldownOptions` (not `rollupOptions`), so the HTML entry override
    // has to live there too — under `rollupOptions` it is silently ignored and
    // Vite falls back to the default index.html (→ main.tsx, the host entry).
    rolldownOptions: {
      ...(baseConfig.build?.rolldownOptions ?? {}),
      input: path.resolve(__dirname, "index.books.html"),
    },
  },
});
