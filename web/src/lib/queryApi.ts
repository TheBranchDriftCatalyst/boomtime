// queryApi.ts — typed client for the cross-domain query DSL (gaka-174.q).
//
// One endpoint: POST /api/v1/query. The request is a QuerySpec that mirrors the
// backend grammar (from(domain)·where·group·measure·over·bucket·having·sort·
// limit — internal/query + internal/queryapi/spec.go); the response is a
// discriminated QueryResult union keyed by `kind`.
//
// This file deliberately does NOT live in api.ts (avoids merge churn on the big
// shared module) but reuses its cleanly-exported primitives — `buildUrl` +
// `ApiError` — and the shared `authStore` so the credential/auth-header/cookie
// behavior is byte-identical to every other authenticated call.
import { authStore } from "@/features/auth/auth";
import { ApiError, buildUrl } from "./api";

// --- Request spec ------------------------------------------------------------

export type QueryDomain = "coding" | "reading";
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
 * unauthenticated, 404 when the reading domain is feature-gated off).
 */
export async function runQuery(spec: QuerySpec): Promise<QueryResult> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  const authHeader = authStore.authHeader();
  if (authHeader) headers["Authorization"] = authHeader;

  const res = await fetch(buildUrl("/api/v1/query"), {
    method: "POST",
    headers,
    body: JSON.stringify(spec),
    credentials: "include", // send/receive the HttpOnly refresh_token cookie
  });

  const text = await res.text();
  let data: unknown = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }

  if (!res.ok) {
    const message =
      (data as { message?: string; error?: string })?.message ||
      (data as { error?: string })?.error ||
      res.statusText ||
      "Query failed";
    throw new ApiError(res.status, message, data);
  }

  return normalizeResult(data as QueryResultWire);
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
