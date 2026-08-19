// Google Analytics 4 (gtag.js) — self-contained, reusable module.
//
// Drop this file into any Vite app and set VITE_GA_MEASUREMENT_ID (G-XXXXXXXXXX)
// at build time. Without the env var every function is a silent no-op, so dev
// builds and self-hosted deployments send nothing. The gtag.js script is only
// injected once init runs with a valid ID.
//
// SPA note: automatic page_view is disabled at init; report route changes via
// trackPageview() (see components/AnalyticsTracker.tsx for the react-router glue).

declare global {
  interface Window {
    dataLayer: unknown[];
    gtag: (...args: unknown[]) => void;
  }
}

const MEASUREMENT_ID: string | undefined = import.meta.env.VITE_GA_MEASUREMENT_ID;

let initialized = false;

/** Inject gtag.js and configure GA. Safe to call unconditionally; no-ops
 *  without a measurement ID and on repeat calls. */
export function initAnalytics(measurementId: string | undefined = MEASUREMENT_ID): void {
  if (initialized || !measurementId) return;

  window.dataLayer = window.dataLayer || [];
  window.gtag = function gtag(...args: unknown[]) {
    window.dataLayer.push(args);
  };
  window.gtag("js", new Date());
  // send_page_view: false — SPA route changes are reported by trackPageview.
  window.gtag("config", measurementId, { send_page_view: false });

  const script = document.createElement("script");
  script.async = true;
  script.src = `https://www.googletagmanager.com/gtag/js?id=${encodeURIComponent(measurementId)}`;
  document.head.appendChild(script);

  initialized = true;
}

/** Report an SPA page view (call on every route change). */
export function trackPageview(path: string, title?: string): void {
  if (!initialized) return;
  window.gtag("event", "page_view", {
    page_path: path,
    page_location: window.location.origin + path,
    ...(title ? { page_title: title } : {}),
  });
}
