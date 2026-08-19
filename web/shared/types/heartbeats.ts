// Heartbeats explorer / source-health / derived-data types mirroring the Go
// backend JSON payloads.

// Whitelisted axes to group/filter heartbeats by. `day` is a "YYYY-MM-DD"
// bucket; the rest map directly to HeartbeatRow columns.
export type HeartbeatAxis =
  | "day"
  | "project"
  | "language"
  | "editor"
  | "plugin"
  | "platform"
  | "machine"
  | "branch"
  | "category"
  | "type"
  | "entity"
  | "isWrite"
  | "userAgent";

// Accumulated group filters keyed by axis. For `day` the value is "YYYY-MM-DD".
export type HeartbeatFilters = Partial<Record<HeartbeatAxis, string>>;

export interface HeartbeatRow {
  id: number;
  time: string;
  entity: string;
  type: string;
  project: string | null;
  language: string | null;
  editor: string | null;
  plugin: string | null;
  platform: string | null;
  machine: string | null;
  branch: string | null;
  category: string | null;
  isWrite: boolean | null;
  lineno: number | null;
  cursorpos: string | null;
  fileLines: number | null;
  dependencies: string[] | null;
  userAgent: string | null;
}

interface HeartbeatGroup {
  value: string | null;
  count: number;
  // Attributed coding time for the group (gap-coalesced within timeLimit).
  seconds: number;
  firstSeen: string;
  lastSeen: string;
}

export interface HeartbeatGroupPayload {
  groupBy: HeartbeatAxis;
  groups: HeartbeatGroup[];
  // The group query is capped server-side; true when more groups were dropped.
  truncated?: boolean;
}

export interface HeartbeatListPayload {
  items: HeartbeatRow[];
  total: number;
  page: number;
  limit: number;
}

// GET /api/v1/users/current/heartbeats/latest -> most-recent heartbeat marker.
export interface LatestHeartbeatPayload {
  lastHeartbeat: string | null; // ISO timestamp of the newest heartbeat
  count: number;
}

// GET /api/v1/users/current/sources/health -> per (plugin, machine) pair with
// its last check-in. The plugin is what actually sends heartbeats, scoped per
// machine so each physical setup is a distinct source. status (active/idle/
// stale/silent) is derived CLIENT-side from lastSeen — see deriveSourceStatus.
export interface SourceHealth {
  plugin: string; // the wakatime plugin (heartbeat source)
  machine: string; // machine name ('unknown' when unset)
  lastSeen: string; // ISO timestamp of the most recent heartbeat
  count: number; // total heartbeats from this pair
}

// GET /api/v1/users/current/derived/status — health of the precomputed
// gap_seconds column + hb_rollup_daily rollup vs the raw heartbeats.
export interface DerivedStatus {
  heartbeats: number;
  gapPopulated: number;
  gapMissing: number;
  rollupRows: number;
  rollupSeconds: number;
  rawSeconds: number;
  inSync: boolean;
  heartbeatsBytes: number;
  rollupBytes: number;
  dbBytes: number;
  // Per-index sizes for the heartbeats table, largest first. Surfaced so the
  // operator can see the storage cost of the trigram / text_pattern_ops
  // indexes shipped for gaka-o4m alongside the older sender/time btrees.
  heartbeatsIndexes: IndexSize[];
}

export interface IndexSize {
  name: string;
  bytes: number;
}

// gaka-90x — Entity Explorer.
export type EntityType = "file" | "app" | "domain" | "url";

export interface EntitySummary {
  entity: string;
  count: number;
  firstSeen: string;
  lastSeen: string;
}

export interface EntityListPayload {
  entities: EntitySummary[];
  truncated: boolean;
}

export interface EntityRedactPayload {
  redacted: number;
}

// POST /api/v1/users/current/db/import — what a whole-database restore loaded.
export interface RestoreSummary {
  gooseVersion: number;
  totalRows: number;
  tables: Record<string, number>;
}
