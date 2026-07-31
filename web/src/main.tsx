import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { RootLayout } from "@/app/App";
import { CatalystProvider } from "@thebranchdriftcatalyst/catalyst-ui/contexts/CatalystProvider";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { authStore } from "@/features/auth/auth";
import "@/index.css";

// Cross-tab logout: when another tab writes the "logout" key, clear this tab.
window.addEventListener("storage", (event) => {
  if (event.key === "logout") authStore.clear();
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

// gaka-ie3: migrated from <BrowserRouter> + <Routes> to
// createBrowserRouter + <RouterProvider> so the data-router-only APIs
// (useBlocker, unstable_usePrompt) are available. The route table is
// declared in @/app/App (`ROUTES`); RootLayout wraps every child with
// AuthProvider + AnalyticsTracker via <Outlet/>.
const router = createBrowserRouter([
  {
    element: <RootLayout />,
    children: [
      // The single catchall — the app's Routes decide what to render.
      // A future refactor could push those definitions up into this
      // config table for loader-based data fetching; today's App.tsx
      // still uses nested <Routes> which just work as a leaf.
      { path: "*", lazy: () => import("@/app/App").then((m) => ({ Component: m.AppRoutes })) },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {/*
      One-shot migration of the legacy `boomtime-theme` key ("dark" | "light")
      into catalyst-ui's `theme:variant`, plus seeding `theme:name` to
      "boomtime" on the very first visit. All handled inside
      <CatalystProvider> — see @thebranchdriftcatalyst/catalyst-ui/contexts/CatalystProvider.
    */}
    <CatalystProvider defaultTheme="boomtime" legacyStorageKey="boomtime-theme">
      {/*
        Radix Tooltip needs a single root-level provider — every <Tooltip>
        under it inherits the delayDuration. 200ms is snappier than the
        700ms default and reads better with the LabelChip hover pattern
        (labels are dense, want the explainer to appear quickly).
      */}
      <TooltipProvider delayDuration={200}>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
          <Toaster position="top-right" richColors />
        </QueryClientProvider>
      </TooltipProvider>
    </CatalystProvider>
  </StrictMode>,
);
