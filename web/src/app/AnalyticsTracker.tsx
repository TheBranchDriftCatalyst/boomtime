import { useEffect } from "react";
import { useLocation } from "react-router";
import { initAnalytics, trackPageview } from "@/lib/analytics";

// Mount once anywhere inside the router. Initializes GA (no-op without
// VITE_GA_MEASUREMENT_ID) and reports a page_view on every route change.
export function AnalyticsTracker() {
  const { pathname, search } = useLocation();

  useEffect(() => {
    initAnalytics();
  }, []);

  useEffect(() => {
    trackPageview(pathname + search);
  }, [pathname, search]);

  return null;
}
