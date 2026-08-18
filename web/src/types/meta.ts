// Meta endpoints: build/version disclosure + embedded changelog.
// Mirrors internal/handler/meta.go.

export interface VersionResponse {
  version: string;
}

// Public client-config advertisement — GET /api/v1/config/public (gaka-93f.1.1).
// Mirrors internal/meta/config_public.go PublicConfigResponse. Non-sensitive:
// only modes/flags the FE needs at boot to pick the auth + onboarding flow.
export interface PublicConfig {
  registration_enabled: boolean;
  auth_provider: string; // "local" | "oidc"
  oidc_enabled: boolean;
  billing_enabled: boolean;
  beta_flags: Record<string, boolean>; // e.g. { user_registration: true }
  // gaka-2ip Phase 1: per-user GitHub connect. true ONLY when the server gate
  // is on AND the OAuth-App creds + state signing key are configured. The
  // GitHubConnectCard renders nothing when false, so the surface is inert.
  github_connect_enabled: boolean;
  // BOOM_FEATURE_BOOKS: catalyst-books/audiobooks + the shared Amazon connect
  // flow. The AmazonConnectCard renders nothing when false.
  books_enabled: boolean;
}

// GET /api/v1/amazon — the shared Amazon device-connect status (catalyst-books
// + catalyst-audiobooks). Never carries the credential.
export interface AmazonConnection {
  connected: boolean;
  status?: string;
  checkedAt?: string;
}

// GET /api/v1/books/items — one siloed reading_items row (catalyst-books /
// catalyst-audiobooks). Mirrors internal/identity readingItemDTO; NEVER carries
// the raw source blob.
// The canonical reading-status vocabulary — EXACT strings, 1:1 with Hardcover's
// enum (want=1 / reading=2 / read=3 / paused=4 / dnf=5). One shared set drives
// the status pill, the editable dropdown, the group values, AND the filter, so
// filter labels == group values == pill labels == Hardcover names (gaka-books).
export const BOOK_STATUSES = [
  "want",
  "reading",
  "read",
  "paused",
  "dnf",
] as const;
export type BookStatus = (typeof BOOK_STATUSES)[number];

// Body of PATCH /api/v1/books/items/:id/curation (gaka-books). Every field is
// optional: only present keys are written, so a rating edit never disturbs the
// status override. `null` on rating/finishedAt CLEARS that override (revert to
// the derived layer). status is always one of the 5 canonical values.
export interface CurationPatch {
  status?: BookStatus;
  rating?: number | null;
  finishedAt?: string | null;
}

// gaka-books: state of the SERVER-side reading monitor (the persistent engine
// that polls each in-progress Kindle book's furthest-page-read and toasts on a
// reading ping). The admin panel is a thin control over this: GET reads it, PUT
// flips `enabled` / `mode`. `mode` picks toast frequency — debounced = one toast
// per book advance; verbose = a toast on every ping. `activeBooks` is how many
// in-progress books the engine is currently watching; `lastPingAt` is when it
// last observed a reading ping (null before the first).
export type ReadingMonitorMode = "debounced" | "verbose";

// Empirically-derived optimal poll intervals (gaka-books). Computed server-side
// from observed whispersync advances so the admin panel can STATE the answer to
// "what's the optimal polling timeframe" in plain English, not just link out.
//   detectSecs        — how often to sweep ALL books for a new advance;
//   captureSecs       — fast-capture cadence for an actively-advancing book;
//   idleSecs          — mark a book idle after this long with no advance;
//   medianAdvanceSecs — median gap between observed whispersync pushes;
//   p90AdvanceSecs    — 90th-percentile gap (the slow tail);
//   sampleCount       — advances the recommendation was derived from.
// The whole object is null when sampleCount is too low to recommend anything.
//
// Diagnostic-mode classification (rm2): the calibration window also fingerprints
// HOW whispersync ships position, so the recommendation states not just the
// intervals but the shape of the data behind them.
//   syncPattern    — 'continuous' (steady position stream) | 'session-boundary'
//                    (bursts only when a reading session ends) | 'unknown';
//   impliedMethod  — the cadence-measurement that pattern implies: 'gap-sum'
//                    (sum inter-advance gaps) | 'position-delta' (diff furthest
//                    location) | 'unknown';
//   rationale      — one plain-English sentence explaining the classification.
export interface ReadingMonitorRecommendation {
  detectSecs: number;
  captureSecs: number;
  idleSecs: number;
  medianAdvanceSecs: number;
  p90AdvanceSecs: number;
  sampleCount: number;
  syncPattern: "continuous" | "session-boundary" | "unknown";
  impliedMethod: "gap-sum" | "position-delta" | "unknown";
  rationale: string;
}

export interface ReadingMonitorState {
  enabled: boolean;
  mode: ReadingMonitorMode;
  activeBooks: number;
  lastPingAt: string | null;
  // gaka-books: the optimal-interval answer, or null until enough data.
  recommendation: ReadingMonitorRecommendation | null;
  // rm2 · diagnostic (calibration) mode. When `calibrating` is true a temporary
  // HIGH-FIDELITY window (~10s polling) is running to measure the true sync
  // cadence; `calibratingUntil` is when it auto-expires (RFC3339), null when
  // idle. Server-driven + per-user — closing the tab does NOT cancel it.
  calibrating: boolean;
  calibratingUntil: string | null;
}

// GET /api/v1/books/reading-monitor/status (requireAuth, user-scoped) — the
// lightweight beacon feed behind the global nav indicator. Just enough to light
// the "calibration running" pulse on EVERY page, without pulling the admin-only
// recommendation/raw payload. Polled ~15s from the shared header.
export interface ReadingMonitorStatus {
  enabled: boolean;
  calibrating: boolean;
  calibratingUntil: string | null;
}

// GET /api/v1/admin/books/reading-monitor/raw (requireAdmin) — recent raw
// heartbeat/position samples from BOTH reading sources, the human-readable feed
// that complements the Grafana cadence board.
//   Kindle:  one furthest-page-read observation. `location`/`dloc` are Kindle
//            location units (dloc = Δ since the prior sample); `intervalSecs`
//            is the gap to the prior sample; `creationTime` is when Amazon
//            stamped it.
//   Audible: one day's listening roll-up (`listeningSeconds` on `day`).
export interface ReadingMonitorKindleSample {
  asin: string;
  title: string;
  location: number;
  dloc: number;
  creationTime: string;
  intervalSecs: number;
}
export interface ReadingMonitorAudibleSample {
  title: string;
  day: string;
  listeningSeconds: number;
}
export interface ReadingMonitorRaw {
  kindle: ReadingMonitorKindleSample[];
  audible: ReadingMonitorAudibleSample[];
}

export interface ReadingItemDTO {
  source: string;
  externalId: string;
  title: string;
  authors: string;
  // EFFECTIVE status: override ?? Amazon-derived. One of the canonical 1:1
  // Hardcover statuses — want | reading | read | paused | dnf (gaka-books).
  status: string;
  progressPercent: number;
  finished: boolean;
  startedAt?: string;
  finishedAt?: string;
  rating?: number;
  syncedAt: string;
  // --- Curation override layer (gaka-books) --------------------------------
  // `status`/`rating`/`finishedAt` above are the EFFECTIVE values (override ??
  // derived). These expose the two layers so the FE can show provenance and
  // let a user curate the status/rating/finish that maps to Hardcover:
  //   statusDerived     — raw Amazon-computed status (want|reading|read only);
  //                       the untouched device layer before any override.
  //   statusOverride    — the sticky curation override, or null when none.
  //   statusIsOverride  — true when the effective status came from the override
  //                       layer (user- or Hardcover-sourced), not from Amazon.
  //   ratingOverride / finishedAtOverride — the override layer for those fields.
  // Provenance heuristic: statusIsOverride && hardcoverStatus === status ⇒ the
  // override was adopted FROM Hardcover; statusIsOverride alone ⇒ user-curated.
  statusDerived?: string;
  statusOverride?: string | null;
  statusIsOverride?: boolean;
  ratingOverride?: number | null;
  finishedAtOverride?: string | null;
  // Richer metadata (gaka-books) — optional; a low-fidelity source omits them.
  // Powers the Books page covers + fuller rows.
  coverUrl?: string;
  subtitle?: string;
  series?: string;
  narrators?: string;
  runtimeMin?: number;
  goodreadsRating?: number;
  // Identifiers for precise external linking (gaka-qic0). external_id is the
  // ASIN; amazonAsin is the print/kindle sibling; isbn is NULL for audiobooks.
  isbn?: string;
  amazonAsin?: string;
  // Hardcover match state (migration 00063). Omitted while unmatched — a nil
  // hardcoverBookId is the honest "not matched yet" signal. Populated once the
  // Hardcover match sync resolves the row (then the link goes direct).
  hardcoverBookId?: number | null;
  hardcoverStatus?: string | null;
  hardcoverMatchedAt?: string;
  // The book's Hardcover slug — the /books/<slug> deep-link segment. Preferred
  // over the numeric id, which 404s on Hardcover's book pages (gaka-qic0). NULL
  // until a re-match / re-pull backfills it onto an already-matched row.
  hardcoverSlug?: string | null;
  // Hardcover LIST memberships (migration 00077) — a property of the book, many-to-
  // many. Rendered as chips in the detail panel + a "List" group-by axis. Empty/absent
  // until the Hardcover pull attaches them.
  hardcoverLists?: string[];
}

// GET /api/v1/hardcover — the Hardcover push-target connection status
// (catalyst-books). Mirrors internal/identity/hardcover_connect.go. NEVER
// carries the bearer token — only presence + last-known status + check time.
// status: 'valid' | 'invalid' | 'unknown'. The Jan-1 token reset makes
// 'invalid' a routine "please re-paste" signal.
export interface HardcoverConnection {
  connected: boolean;
  status?: string;
  checkedAt?: string;
}

// One durable notification (migration 00079) — replayed on session start so a
// book-finish fired while offline isn't lost. Mirrors db.Notification's JSON.
export interface NotificationDTO {
  id: number;
  type: string;
  title: string;
  body?: string;
  data?: Record<string, unknown>;
  at: string;
  readAt?: string;
}

// One discrete read of a book (migration 00078) — the Book panel's read history.
// A book can be read more than once; each is a ReadEvent (origin = who produced it).
export interface ReadEvent {
  origin: string; // hardcover | audible | kindle-insights
  source?: string;
  startedAt?: string;
  finishedAt?: string;
  progressPages?: number;
  progressSeconds?: number;
}

// One Hardcover search hit rendered as a pickable card in the manual match-fixer
// (GET /api/v1/hardcover/search). Fields beyond bookId/title are best-effort.
export interface HardcoverCandidate {
  bookId: number;
  title: string;
  authors: string[];
  coverUrl: string;
  year: number;
  slug: string;
}

// Admin caps dashboard — GET /api/v1/admin/users (gaka-93f.6). Mirrors
// internal/admin/users.go adminUsersResponse.
export interface AdminUserRow {
  username: string;
  role: string;
  disabled: boolean;
  capabilities: Record<string, boolean>; // effective (role defaults + overrides)
}
export interface AdminUsersPayload {
  capabilities: string[]; // canonical column order
  roles: Record<string, Record<string, boolean>>; // role -> default caps (legend)
  users: AdminUserRow[];
}

// Admin jobs tab — GET /api/v1/admin/jobs + /schedules (gaka-hney). Mirrors
// internal/admin/jobs.go. Admin-gated (403 for non-admins); the tab is hidden
// from the sidebar for non-admins just like the other admin sections.
export type AdminJobStatus =
  | "queued"
  | "running"
  | "done"
  | "failed"
  | "cancelled";

export interface AdminJob {
  id: number;
  kind: string;
  status: AdminJobStatus;
  attempts: number;
  maxAttempts: number;
  error: string; // "" unless a run failed
  runAt: string; // RFC3339 — when the job becomes eligible to run
  createdAt: string;
  startedAt: string | null; // null until a worker picks it up
  finishedAt: string | null; // null until it terminates (done/failed)
}
export interface AdminJobsPayload {
  jobs: AdminJob[];
}
// One recurring, self-scheduling job kind. `nextRun` is the next fire time;
// `lastRun` is null until the kind has fired at least once this process.
export interface AdminJobSchedule {
  kind: string;
  intervalSeconds: number;
  nextRun: string; // RFC3339
  lastRun: string | null;
}
export interface AdminJobSchedulesPayload {
  schedules: AdminJobSchedule[];
}

// One row of the per-kind queue overview — GET /api/v1/admin/jobs/queues
// (gaka-hney). Mirrors internal/admin.queueKindDTO. queued/running are the live
// depth; maxConcurrency (0 = unlimited) is the registry cap, so running/max is
// the headroom bar and running >= max (with a backlog) is back-pressure.
// doneLastHour/failedLastHour/avgDurationMs are the trailing-hour throughput
// window; lastRunAt/lastStatus are the kind's most-recent activity.
export interface AdminJobQueue {
  kind: string;
  queued: number;
  running: number;
  maxConcurrency: number; // 0 = unlimited
  doneLastHour: number;
  failedLastHour: number;
  avgDurationMs: number; // 0 when nothing finished this hour
  lastRunAt: string | null; // RFC3339, null when the kind has no rows yet
  lastStatus: string; // "" when the kind has no rows yet
}
export interface AdminJobQueuesPayload {
  queues: AdminJobQueue[];
}

// Rate-metric registry — GET /api/v1/admin/metrics (gaka-metrics). Mirrors
// internal/metrics.Series. A generic rolling time-series: any backend call to
// metrics.Inc(name)/Observe(name) appears here as a series, so the FE renders
// it with zero per-metric wiring. `kind` is "counter" (a per-minute rate) or
// "gauge" (last observed value per minute); `points` are oldest→newest,
// per-minute buckets over a ~2h window (idle buckets densified to 0).
export type MetricKind = "counter" | "gauge";
export interface MetricPoint {
  bucket: string; // RFC3339 bucket start (minute-aligned)
  value: number;
}
export interface MetricSeries {
  name: string;
  kind: MetricKind;
  unit?: string;
  points: MetricPoint[];
}
export interface MetricsPayload {
  series: MetricSeries[];
}

// Prometheus gathered view — GET /api/v1/admin/metrics (gaka-metrics, pivoted
// to Prometheus). The endpoint Gather()s internal/metrics.Registry (the SAME
// registry scraped at /metrics for Grafana) and flattens each family into
// {name, help, type, samples}. `type` is the Prometheus metric type
// ("counter" | "gauge" | "histogram" | "summary" | "untyped"). Each sample is
// one label-set: counters/gauges carry `value`; histograms/summaries carry
// `count` + `sum` (the FE derives avg = sum/count, e.g. a latency read-out).
export type MetricType =
  | "counter"
  | "gauge"
  | "histogram"
  | "summary"
  | "untyped";
export interface MetricSample {
  labels?: Record<string, string>;
  value?: number;
  count?: number;
  sum?: number;
}
export interface MetricFamily {
  name: string;
  help?: string;
  type: MetricType;
  samples: MetricSample[];
}
export interface MetricsFamiliesPayload {
  families: MetricFamily[];
}

// Linked external identities — GET /api/v1/users/current/identities (gaka-b5n.4).
export interface LinkedIdentity {
  provider: string;
  email: string;
  subPrefix: string;
  linkedAt: string;
}
export interface IdentitiesPayload {
  identities: LinkedIdentity[];
  oidcAvailable: boolean; // is OIDC configured (link possible)?
  hasPassword: boolean; // does the caller still have a local password?
}

// GitHub connection status — GET /api/v1/users/current/github (gaka-2ip Phase 1).
// Mirrors internal/identity/github_oauth.go githubConnectionResponse. NEVER
// carries the access token — only presence + the (safe) login + last status.
export interface GithubConnection {
  connected: boolean;
  login?: string | null;
  status?: "valid" | "invalid" | "unknown" | null;
  checkedAt?: string | null;
}
