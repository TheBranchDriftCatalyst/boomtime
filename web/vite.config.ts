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
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
    // Dedupe React so a linked catalyst-ui (via yarn link) doesn't ship its
    // own copy alongside boomtime's — mixed instances break hooks.
    dedupe: ["react", "react-dom", "react/jsx-runtime"],
  },
  server: {
    port: 5173,
    host: true, // bind 0.0.0.0 so the dev server is reachable from Docker
    proxy,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // gaka-4hv: split heavyweight vendor libs off the main entry. d3 is by
    // far the biggest single dep (~120kB min); react + react-router + react-
    // query are all long-lived and can share a chunk that browsers cache
    // across dashboard reloads. Route-level pages are chunked separately
    // via React.lazy in src/app/App.tsx.
    rolldownOptions: {
      output: {
        manualChunks: (id) => {
          if (!id.includes("node_modules")) return undefined;
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
    setupFiles: ["./src/test/setup.ts"],
    // Co-located *.test.ts(x) files.
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    css: false,
    restoreMocks: true,
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        "src/**/*.{test,spec}.{ts,tsx}",
        "src/test/**",
        "src/main.tsx",
        "src/**/*.d.ts",
      ],
    },
  },
});
