import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// Proxy the Go backend's path prefixes so the SPA can use same-origin
// relative URLs in dev.
const backend = process.env.HAKA_BACKEND_URL || "http://localhost:8080";
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
});
