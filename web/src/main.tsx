import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import { App } from "@/app/App";
import { AnalyticsTracker } from "@/app/AnalyticsTracker";
import { AuthProvider } from "@/features/auth/useAuth";
import { ThemeProvider } from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";
import { authStore } from "@/features/auth/auth";
import "@/index.css";

// One-shot migration from boomtime's old localStorage key to catalyst-ui's
// keys. `boomtime-theme` used to hold 'dark' | 'light'; catalyst-ui splits
// that into `theme:name` + `theme:variant`. Also seeds `theme:name` to
// 'boomtime' the first time so the theme identity is preserved without
// forcing users through a picker.
try {
  const legacy = window.localStorage.getItem("boomtime-theme");
  if (legacy === "dark" || legacy === "light") {
    if (!window.localStorage.getItem("theme:variant")) {
      window.localStorage.setItem("theme:variant", JSON.stringify(legacy));
    }
    window.localStorage.removeItem("boomtime-theme");
  }
  if (!window.localStorage.getItem("theme:name")) {
    window.localStorage.setItem("theme:name", JSON.stringify("boomtime"));
  }
} catch {
  // localStorage can throw in private mode / disabled cookies — ignore.
}

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
