// Standalone catalyst-books entry (boom-zp2s). BYTE-FOR-BYTE identical to
// main.tsx — same providers (QueryClient, CatalystProvider, TooltipProvider,
// DevProviders, Toaster), same RootLayout, same createBrowserRouter shape, same
// theme ("boomtime" default + legacy migration) — EXCEPT it composes only the
// core + books domains (registerBooksAppDomains) instead of every host domain.
// Because registerBoomtimeDomain is never imported, the boomtime page modules
// never enter this entry's module graph and tree-shake away.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { RootLayout } from "@shared/app/App";
import { RouteErrorBoundary } from "@shared/app/RouteErrorBoundary";
import { reloadOnceForStaleChunk } from "@shared/lib/chunkReload";
import { CatalystProvider } from "@thebranchdriftcatalyst/catalyst-ui/contexts/CatalystProvider";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { DevProviders } from "@shared/features/devtools";
import { authStore } from "@shared/features/auth/auth";
import { registerBooksAppDomains } from "@shared/app/registerBooksApp";
import "@shared/index.css";

// Compose ONLY the standalone books app's domains (core + books) into the
// shared nav / settings / admin / routing seams BEFORE the first render. The
// host entry (main.tsx) composes the full set; this leaner composition is what
// tree-shakes the boomtime code domain out of the books image.
registerBooksAppDomains();

// Cross-tab logout: when another tab writes the "logout" key, clear this tab.
window.addEventListener("storage", (event) => {
  if (event.key === "logout") authStore.clear();
});

// Stale-chunk recovery: after a deploy, a still-open tab imports chunk hashes
// that no longer exist. Vite fires "vite:preloadError" at the fetch layer —
// reload once to fetch the new build (guarded against loops in chunkReload).
window.addEventListener("vite:preloadError", (event) => {
  event.preventDefault();
  reloadOnceForStaleChunk();
});

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
    },
  },
});

const router = createBrowserRouter([
  {
    element: <RootLayout />,
    errorElement: <RouteErrorBoundary />,
    children: [
      {
        path: "*",
        lazy: () => import("@shared/app/App").then((m) => ({ Component: m.AppRoutes })),
        errorElement: <RouteErrorBoundary />,
      },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <CatalystProvider defaultTheme="boomtime" legacyStorageKey="boomtime-theme">
      <TooltipProvider delayDuration={200}>
        <DevProviders>
          <QueryClientProvider client={queryClient}>
            <RouterProvider router={router} />
            <Toaster position="top-right" richColors />
          </QueryClientProvider>
        </DevProviders>
      </TooltipProvider>
    </CatalystProvider>
  </StrictMode>,
);
