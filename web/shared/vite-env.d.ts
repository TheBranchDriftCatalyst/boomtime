/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** GA4 measurement ID (G-XXXXXXXXXX). Unset = analytics disabled. */
  readonly VITE_GA_MEASUREMENT_ID?: string;
  /**
   * Base URL of the Grafana instance that hosts the reading-monitor cadence
   * board (boom-books). Unset = fall back to the deploy default in
   * ReadingMonitorPanel (a same-host /grafana reverse-proxy path). Set this to
   * an absolute origin (e.g. https://grafana.example.com) when Grafana lives
   * elsewhere.
   */
  readonly VITE_GRAFANA_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
