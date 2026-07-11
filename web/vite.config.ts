/// <reference types="vitest/config" />
import { defineConfig } from "vite";
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
  },
  server: {
    port: 5173,
    host: true, // bind 0.0.0.0 so the dev server is reachable from Docker
    proxy,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
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
