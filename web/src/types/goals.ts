// Goals feature types (gaka-wpb) — mirror the backend Go structs in
// internal/stats/goals.go and internal/db/goals.go. The spec is a
// discriminated union walked recursively; the backend validator
// guards it (POST/PATCH return 400 with an error string on any
// invariant break — the FE surfaces that verbatim).

// Axes a `time` leaf may target. Mirrors validHeartbeatAxes in
// internal/stats/goals.go and the raw+rollup axis registry
// (internal/db/axes.go). Kept as its own literal — the existing
// HeartbeatAxis in @/types/api includes more axes (entity, day)
// that goals don't support.
export type GoalHeartbeatAxis =
  | "language"
  | "project"
  | "editor"
  | "category"
  | "branch"
  | "plugin"
  | "machine"
  | "platform";

export type GoalTimeWindow = "day" | "week" | "month" | "year" | "lifetime";
export type GoalActiveDaysWindow = "week" | "month" | "year";
export type GoalOp = ">=" | "<=" | "==";

// Which domain a `time` leaf measures. Mirrors validTimeSources in
// internal/goals/eval.go. Undefined/"coding" → attributed coding seconds from
// hb_rollup_daily (the legacy path, axis-filtered). "reading" → total
// listening-seconds from reading_activity over the window (no axis in v1).
export type GoalTimeSource = "coding" | "reading";

// One node of the predicate tree. Discriminated by `kind`. Each
// variant carries only the fields relevant to it — `omitempty` on
// the Go side means unused fields don't reach the wire, and TS's
// `?:` mirrors that. The recursion is intentional (all/any/not
// contain nested Predicates); the backend caps depth at 5 and
// rejects anything deeper with 400.
export type Predicate =
  | {
      kind: "time";
      // Domain the leaf measures. Omitted = "coding" (the legacy default).
      // A "reading" leaf carries NO axis/value — it sums total listening time.
      source?: GoalTimeSource;
      // axis/value are the coding-path attribution filter. Optional so a
      // reading leaf (which has neither) is representable in the same variant.
      axis?: GoalHeartbeatAxis;
      value?: string | null;
      op: GoalOp;
      target_seconds: number;
      window: GoalTimeWindow;
    }
  | {
      kind: "streak";
      condition: Predicate;
      min_days: number;
    }
  | {
      kind: "active_days";
      op: GoalOp;
      n: number;
      window: GoalActiveDaysWindow;
    }
  | { kind: "all"; of: Predicate[] }
  | { kind: "any"; of: Predicate[] }
  | { kind: "not"; of: [Predicate] };

// GET /api/v1/users/current/goals -> {goals:[Goal]}.
// Timestamps are RFC3339 strings. lastEvaluatedAt is null when the
// cache is empty (never computed OR invalidated); the FE reads that
// to render a "computing…" state on tiles.
export interface Goal {
  id: string;
  owner: string;
  name: string;
  description: string | null;
  spec: Predicate;
  enabled: boolean;
  // Public gates the goal-progress/goal-ring/goal-list embeddable widgets
  // (Part B Stage 4, internal/widgets.WidgetSvg): a goal is included in
  // the owner's public embed iff enabled && public. Defaults false —
  // goals stay private until the owner opts a specific goal in via the
  // "Public" toggle. Independent of `enabled`.
  public: boolean;
  createdAt: string;
  updatedAt: string;
  lastEvaluatedAt: string | null;
  // The cached Progress. May be null when the cache is empty; the
  // FE never relies on this for display — it always hits
  // /goals/:id/progress or the batched form. Kept typed here so
  // debug tooling can surface the cached payload.
  lastProgress: GoalProgress | null;
}

// One leaf's evaluated snapshot in the returned Progress. Group
// nodes (all/any/not/streak) roll up their contribution via the
// outer Progress.hit / Progress.progress fields; only actual
// leaves + the terminal reduce-to-scalar summaries (streak,
// active_days) surface as SubConditions.
export interface GoalSubCondition {
  kind: "time" | "active_days" | "streak";
  // Present on reading-source time sub-conditions ("reading"); omitted on the
  // coding path. Mirrors SubCondition.Source in internal/goals/eval.go.
  source?: GoalTimeSource;
  axis?: GoalHeartbeatAxis;
  value?: string | null;
  op?: GoalOp;
  window?: string;
  current: number;
  target: number;
  progress: number;
  hit: boolean;
}

export interface GoalProgress {
  hit: boolean;
  progress: number; // 0..1
  sub_conditions: GoalSubCondition[];
}

// POST /api/v1/users/current/goals body — server validates spec.
export interface CreateGoalBody {
  name: string;
  description?: string;
  spec: Predicate;
  // Omitted/false = private, matching the server-side column default.
  public?: boolean;
}

// PATCH /api/v1/users/current/goals/:id — any subset of fields.
export interface UpdateGoalBody {
  name?: string;
  description?: string;
  spec?: Predicate;
  enabled?: boolean;
  public?: boolean;
}

// GET /api/v1/users/current/goals/progress -> {progress: {id: Progress}}.
export interface BatchGoalProgress {
  progress: Record<string, GoalProgress>;
}
