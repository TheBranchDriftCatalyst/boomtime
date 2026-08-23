import { defineConfig } from "vite";
import type { ViteUserConfig } from "vitest/config";

// Vitest 3 bundles its own vite 7 for typing, so its built-in
// `declare module "vite"` augmentation (which adds the `test` key) lands on
// that nested copy — never on this project's vite 8. Re-apply the same
// augmentation against our vite here, typed by vitest's real InlineConfig, so
// the `test` block below is fully type-checked without any casts.
declare module "vite" {
  interface UserConfig {
    /** Options for Vitest. */
    test?: ViteUserConfig["test"];
  }
}
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { catalystPlugin } from "@thebranchdriftcatalyst/catalyst-ui/vite";
import path from "node:path";

// Proxy the Go backend's path prefixes so the SPA can use same-origin
// relative URLs in dev.
const backend = process.env.BOOM_BACKEND_URL || "http://localhost:8080";
const proxy = Object.fromEntries(
  ["/api", "/auth", "/badge", "/import"].map((p) => [
    p,
    {
      target: backend,
      changeOrigin: true,
      secure: false,
      // Proxy WebSocket upgrades too — import job log streaming lives at
      // /import/jobs/:id/ws. Vite's HMR socket uses a separate internal path,
      // so enabling ws here does not interfere with HMR.
      ws: true,
    },
  ]),
);

export default defineConfig({
  // `catalystPlugin()` unions React deduplication into `resolve.dedupe`,
  // registers a Tailwind `@source` pointing at the resolved catalyst-ui
  // dist path, AND injects the no-flash-of-wrong-theme script into
  // `index.html`'s <head> (mirrors <CatalystProvider>'s legacyStorageKey
  // migration so the pre-mount paint matches the post-mount state).
  plugins: [
    react(),
    tailwindcss(),
    catalystPlugin({ noFlash: { legacyStorageKey: "boomtime-theme" } }),
  ],
  // boom-zp2s / boom-abg0 Step B: the colocated books FE lives OUTSIDE this Vite
  // root (internal/books/web/src). esbuild's per-file tsconfig discovery only
  // walks up from the file, so it never finds web/tsconfig's `jsx: "react-jsx"`
  // for those out-of-root .tsx files and falls back to the classic runtime —
  // which emits bare `React.createElement` and throws "React is not defined" at
  // runtime/in Vitest. Pin the automatic runtime globally so in-root and
  // colocated .tsx transform identically.
  esbuild: {
    jsx: "automatic",
    jsxImportSource: "react",
  },
  resolve: {
    alias: {
      "@shared": path.resolve(__dirname, "./shared"),
      // boom-zp2s / boom-abg0 Step B: the books domain FE is PHYSICALLY
      // colocated with its Go package under internal/books/web/src (outside
      // this Vite root). `@books/*` resolves there so both the host build and
      // the standalone books build (vite.books.config.ts, which extends this
      // config) source the exact same books tree. Vite's string aliases are
      // boundary-aware (`@` compiles to /^@(\/|$)/), so `@books/...` never
      // gets swallowed by the `@` entry above regardless of order.
      "@books": path.resolve(__dirname, "../internal/books/web/src"),
      // boom-zp2s / boom-abg0 Step B Phase 2: the boomtime (code) domain FE is
      // PHYSICALLY colocated with its Go package under internal/boomtime/web/src
      // (outside this Vite root). `@boomtime/*` resolves there. Only the host
      // build (vite.config.ts) registers the boomtime domain; the standalone
      // books build (vite.books.config.ts) never imports it, so it tree-shakes
      // out. Vite string aliases are boundary-aware (`@` compiles to /^@(\/|$)/),
      // so `@boomtime/...` is never swallowed by the `@` entry above.
      "@boomtime": path.resolve(__dirname, "../internal/boomtime/web/src"),
      // Part B Stage 2 (boom-174.x): the canonical widget spec registry is
      // ONE committed file — Go embeds it (internal/widget/spec.go), the FE
      // reads the exact same bytes through this alias (see
      // src/features/widgets/specs.ts) instead of a generated copy.
      "@widget-specs": path.resolve(__dirname, "../internal/boomtime/widget/specs.json"),
    },
    dedupe: ["react", "react-dom", "react/jsx-runtime"],
  },
  server: {
    port: 5173,
    host: true, // bind 0.0.0.0 so the dev server is reachable from Docker
    proxy,
    // The books FE lives under ../internal/books/web (outside this Vite root),
    // reached via the `@books` alias above. Allow the dev server to serve files
    // from the repo root so those out-of-root modules load in `yarn dev`.
    // (Production `vite build` doesn't enforce fs.allow — it's a dev-only guard
    // — but the widget-specs JSON already imports from ../internal, so keeping
    // the allowance explicit documents the cross-root reach.)
    fs: {
      allow: [path.resolve(__dirname, "..")],
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // boom-4hv / boom-93f.23: split heavyweight vendor libs off the main entry
    // AND coalesce the long tail of sub-2KB chunks. Before this, a page load
    // fetched 50+ tiny files: one chunk per lucide icon, one per lazy theme,
    // and one per catalyst-ui UI primitive. We group those into a handful of
    // shared chunks while KEEPING the big long-lived vendors (react, radix,
    // d3, react-three-fiber) and the per-route lazy pages split.
    rolldownOptions: {
      output: {
        // Canonical catalyst-ui theme names (mirrors THEME_REGISTRY). Each is a
        // lazily `import()`-ed CSS-in-JS chunk in dist/lib/chunks/<name>-<hash>.js.
        // They only load on a theme switch (never in the initial modulepreload
        // set), so collapsing all ten into one `themes` chunk leaves the initial
        // payload untouched — it just trades ten on-demand fetches for one.
        manualChunks: (id) => {
          if (!id.includes("node_modules")) return undefined;

          // ── lucide icons ────────────────────────────────────────────────
          // ~30 icons, each its own <200B chunk today because they are shared
          // across route chunks. Fold every lucide glyph into one `icons`
          // chunk. Checked before the react rules so lucide-react never falls
          // through to vendor-react.
          if (/node_modules\/lucide-react\//.test(id)) {
            return "icons";
          }

          // ── catalyst-ui: themes, icons, and small UI primitives ─────────
          const cat = id.match(
            /node_modules\/@thebranchdriftcatalyst\/catalyst-ui\/dist\/lib\/(.*)$/,
          );
          if (cat) {
            const rel = cat[1];
            // catalyst's own pre-split big vendors (vendor-radix ~215kB,
            // vendor-react, vendor-forms, vendor-utils) stay SEPARATE.
            if (/^chunks\/vendor-/.test(rel)) return undefined;
            // Lazy theme chunks → one `themes` chunk.
            if (
              /^chunks\/(catalyst|dracula|gold|laracon|nature|netflix|nord|dungeon|boomtime|arasaka)-[A-Za-z0-9_]+\.js$/.test(
                rel,
              )
            ) {
              return "themes";
            }
            // catalyst re-exported lucide glyphs (createLucideIcon + single
            // icon chunks that pull it in) → the same `icons` chunk.
            if (/^chunks\/createLucideIcon-/.test(rel)) return "icons";
            // Small shared UI primitives (button, card, dialog, badge, …) live
            // in dist/lib/ui/* and each split to a sub-2KB chunk. `calendar` is
            // the one heavyweight (react-day-picker, ~60kB) and stays its own
            // lazy chunk. Everything else in ui/ folds into `vendor-catalyst`.
            if (/^ui\//.test(rel) && !/^ui\/calendar\b/.test(rel)) {
              return "vendor-catalyst";
            }
            return undefined;
          }

          if (id.includes("d3-") || id.match(/node_modules\/d3\//)) {
            return "vendor-d3";
          }
          if (
            id.includes("react-router") ||
            id.includes("@tanstack/react-query") ||
            /node_modules\/react-dom\//.test(id) ||
            /node_modules\/react\//.test(id)
          ) {
            return "vendor-react";
          }
          if (id.includes("@radix-ui")) {
            return "vendor-radix";
          }
          return undefined;
        },
      },
    },
    chunkSizeWarningLimit: 600,
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./shared/test/setup.ts"],
    // Co-located *.test.ts(x) files — in this Vite root's shared bucket
    // (shared/) AND the books FE colocated with its Go package under
    // internal/books/web/src (boom-zp2s / boom-abg0 Step B).
    include: [
      "shared/**/*.{test,spec}.{ts,tsx}",
      "../internal/books/web/src/**/*.{test,spec}.{ts,tsx}",
      "../internal/boomtime/web/src/**/*.{test,spec}.{ts,tsx}",
    ],
    css: false,
    restoreMocks: true,
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      include: [
        "shared/**/*.{ts,tsx}",
        "../internal/books/web/src/**/*.{ts,tsx}",
        "../internal/boomtime/web/src/**/*.{ts,tsx}",
      ],
      exclude: [
        "shared/**/*.{test,spec}.{ts,tsx}",
        "../internal/books/web/src/**/*.{test,spec}.{ts,tsx}",
        "../internal/boomtime/web/src/**/*.{test,spec}.{ts,tsx}",
        "shared/test/**",
        "shared/main.tsx",
        "shared/**/*.d.ts",
      ],
    },
  },
});
