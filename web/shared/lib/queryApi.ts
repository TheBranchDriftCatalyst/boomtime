// queryApi.ts — typed client for the cross-domain query DSL (boom-174.q).
//
// One endpoint: POST /api/v1/query. The request is a QuerySpec that mirrors the
// backend grammar (from(domain)·where·group·measure·over·bucket·having·sort·
// limit — internal/query + internal/queryapi/spec.go); the response is a
// discriminated QueryResult union keyed by `kind`.
//
// This file deliberately does NOT live in api.ts (avoids merge churn on the big
// shared module) but issues its one call through api.ts's exported `request()`
// so the credential/auth-header/cookie behavior — and the single-flight
// refresh + one-retry on 401 — is byte-identical to every other authenticated
// call. It used to hand-roll its own fetch, which meant an expired access token
// surfaced as a hard 401 to query-DSL widgets while the rest of the page
// silently recovered (boom-28vm audit).
import { ApiError, request } from "./api";

// --- Request spec ------------------------------------------------------------

// "readingEvents" is the reading-EVENTS domain (measure `reads` over the
// reading_events_enriched view) — a book's discrete reads, one row per read, as
// opposed to "reading" (the library, one row per book). Books-gated like "reading".
export type QueryDomain = "coding" | "reading" | "readingEvents";
export type Granularity = "none" | "day" | "week" | "month";
// "ilike" is a case-insensitive substring match (server compiles it to SQL
// ILIKE '%value%' with the value bound as an arg — injection-safe like eq).
export type PredicateOp = "eq" | "neq" | "in" | "ilike";
export type HavingOp = ">=" | "<=" | ">" | "<" | "==" | "!=";
export type SortField = "measure" | "value" | "bucket" | "key" | (string & {});

// PredicateNode mirrors query.Predicate: a leaf {dim, op, values} or a boolean
// combinator (and/or/not) over child nodes.
export type PredicateNode =
  | { kind: "leaf"; dim: string; op: PredicateOp; values: string[] }
  | { kind: "and" | "or"; of: PredicateNode[] }
  | { kind: "not"; of: [PredicateNode] };

// RangeSpec bounds a query in time. Set at most one of lastN / between; the unit
// of lastN is the enclosing granularity (days when granularity is "none").
export interface RangeSpec {
  lastN?: number;
  between?: { start: string; end: string }; // RFC3339 timestamps
}

export interface OverSpec {
  granularity?: Granularity; // defaults to "none"
  range?: RangeSpec;
}

export interface BucketSpec {
  topN: number;
  pin?: string[];
  other?: boolean;
}

export interface HavingSpec {
  op: HavingOp;
  value: number;
}

export interface SortSpec {
  field: SortField;
  desc?: boolean;
}

export interface QuerySpec {
  domain: QueryDomain;
  measure: string;
  where?: PredicateNode;
  group?: string;
  over?: OverSpec;
  bucket?: BucketSpec;
  having?: HavingSpec;
  sort?: SortSpec;
  limit?: number;
  // rollups requests extra per-group measures alongside the grouped measure;
  // each lands in GroupRow.stats (with an always-present "count").
  rollups?: string[];
  // rows switches to leaf-rows mode (no aggregate): the entity rows under the
  // where predicate, owner-scoped + paginated by page. Returns a `rows` result.
  rows?: boolean;
  page?: { number: number; size: number };
}

// --- Response union ----------------------------------------------------------

export interface SeriesPoint {
  bucket: string; // RFC3339 UTC
  value: number;
}

export interface GroupRow {
  key: string;
  value: number;
  // Present only for a rollups query: count is the group's row count, stats the
  // per-measure rollups (count included).
  count?: number;
  stats?: Record<string, number>;
}

// QueryResult is discriminated on `kind`. Exactly one payload arm is present;
// the array arms default to [] when the result set is empty.
export type QueryResult =
  | { kind: "scalar"; scalar: number }
  | { kind: "series"; series: SeriesPoint[] }
  | { kind: "groups"; groups: GroupRow[] }
  | { kind: "rows"; rows: Record<string, unknown>[]; total: number };

// Raw wire shape (optional arms) before we normalize to the union above.
interface QueryResultWire {
  kind: "scalar" | "series" | "groups" | "rows";
  scalar?: number;
  series?: SeriesPoint[];
  groups?: GroupRow[];
  rows?: Record<string, unknown>[];
  total?: number;
}

// --- Client ------------------------------------------------------------------

/**
 * runQuery POSTs a spec to /api/v1/query and returns the typed result.
 *
 * Throws ApiError on a non-2xx response (e.g. 400 for an unknown
 * domain/measure/dimension the backend registry rejects, 401 when
 * unauthenticated AND the refresh also fails, 404 when the reading domain is
 * feature-gated off). A merely-expired access token is refreshed and retried
 * once inside request(), exactly like every api.* call.
 */
export async function runQuery(spec: QuerySpec): Promise<QueryResult> {
  const wire = await request<QueryResultWire>("/api/v1/query", {
    method: "POST",
    body: spec,
  });
  return normalizeResult(wire);
}

// normalizeResult collapses the optional wire arms into the discriminated union,
// defaulting empty array arms so consumers can rely on a present payload.
function normalizeResult(wire: QueryResultWire): QueryResult {
  switch (wire.kind) {
    case "scalar":
      return { kind: "scalar", scalar: wire.scalar ?? 0 };
    case "series":
      return { kind: "series", series: wire.series ?? [] };
    case "groups":
      return { kind: "groups", groups: wire.groups ?? [] };
    case "rows":
      return { kind: "rows", rows: wire.rows ?? [], total: wire.total ?? 0 };
    default:
      throw new ApiError(500, `unexpected query result kind: ${(wire as { kind: string }).kind}`, wire);
  }
}
