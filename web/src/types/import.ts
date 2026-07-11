// Import (Wakatime backfill jobs) + server-log stream types mirroring the Go
// backend JSON payloads.

export interface ImportRequest {
  // base64-encoded before sending. Omitted entirely when the user leaves the
  // token blank so the server falls back to its env-configured key.
  apiToken?: string;
  startDate: string; // ISO
  endDate: string; // ISO
}

// GET /import/config -> whether the server has a Wakatime key configured.
export interface ImportConfigPayload {
  hasServerKey: boolean; // TODO verify key
}

// POST /import/wakatime-range -> how far back the user's wakatime.com data goes.
export interface WakatimeRangePayload {
  startDate: string; // YYYY-MM-DD
  endDate: string; // YYYY-MM-DD
  totalSeconds: number;
  text: string; // human-readable duration, e.g. "3 mos 4 days"
  hasData: boolean;
}

// --- First-class import jobs (durable, streamed over WebSocket) --------------

export type ImportJobState =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface ImportJob {
  id: number;
  owner: string;
  state: ImportJobState;
  startDate: string;
  endDate: string;
  totalDays: number;
  processedDays: number;
  importedCount: number;
  currentDay: string | null;
  error: string | null;
  createdAt: string;
  startedAt: string | null;
  finishedAt: string | null;
  // gaka-unq.1: wakatime.com API schema-drift findings observed during the
  // run. Undefined/null when clean. Warning severity => banner; error severity
  // => banner + red tint.
  drift?: DriftFinding[] | null;
}

// gaka-unq.1: one wakatime.com API schema-drift finding. Mirrors the Go
// DriftFinding struct in internal/importer/drift.go.
export interface DriftFinding {
  endpoint: string;
  kind:
    | "unknown_field"
    | "missing_required"
    | "type_changed"
    | "envelope_changed";
  field: string; // "" when kind == envelope_changed
  detail: string;
  severity: "warning" | "error";
  firstSeenDay?: string; // "" for lookups
  count: number;
}

export interface ImportLogLine {
  id: number;
  ts: string;
  level: string;
  message: string;
}

export interface SubmitImportResponse {
  jobId: number;
  jobStatus: string;
}

export interface CancelImportPayload {
  job: ImportJob;
}

// WebSocket messages the server pushes to the client.
export type ImportSocketMessage =
  | { type: "snapshot"; job: ImportJob; logs: ImportLogLine[] }
  | { type: "log"; log: ImportLogLine }
  | { type: "progress"; job: ImportJob }
  | { type: "state"; job: ImportJob };

export function isTerminalState(state: ImportJobState): boolean {
  return (
    state === "completed" || state === "failed" || state === "cancelled"
  );
}

// --- Server process logs (the "Logs" tab) ------------------------------------

// One captured slog record from the running server process. `id` is a
// process-monotonic cursor used for backfill/resume (afterId).
export interface ServerLogEntry {
  id: number;
  time: string;
  level: string;
  msg: string;
  attrs?: Record<string, string> | null;
}

// WebSocket messages the server pushes on /api/v1/logs/ws.
export type ServerLogSocketMessage =
  | { type: "snapshot"; logs: ServerLogEntry[] }
  | { type: "log"; log: ServerLogEntry };
