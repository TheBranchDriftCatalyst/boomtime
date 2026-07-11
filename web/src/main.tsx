import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { App } from "@/App";
import { AnalyticsTracker } from "@/components/AnalyticsTracker";
import { AuthProvider } from "@/hooks/useAuth";
import { ThemeProvider } from "@/theme/ThemeProvider";
import { authStore } from "@/lib/auth";
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
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <AnalyticsTracker />
          <AuthProvider>
            <App />
            <Toaster position="top-right" richColors />
          </AuthProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
