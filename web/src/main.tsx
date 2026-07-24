import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { App } from "@/app/App";
import { AnalyticsTracker } from "@/app/AnalyticsTracker";
import { AuthProvider } from "@/features/auth/useAuth";
import { CatalystProvider } from "@thebranchdriftcatalyst/catalyst-ui/contexts/CatalystProvider";
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

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {/*
      One-shot migration of the legacy `boomtime-theme` key ("dark" | "light")
      into catalyst-ui's `theme:variant`, plus seeding `theme:name` to
      "boomtime" on the very first visit. All handled inside
      <CatalystProvider> — see @thebranchdriftcatalyst/catalyst-ui/contexts/CatalystProvider.
    */}
    <CatalystProvider defaultTheme="boomtime" legacyStorageKey="boomtime-theme">
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <AnalyticsTracker />
          <AuthProvider>
            <App />
            <Toaster position="top-right" richColors />
          </AuthProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </CatalystProvider>
  </StrictMode>,
);
