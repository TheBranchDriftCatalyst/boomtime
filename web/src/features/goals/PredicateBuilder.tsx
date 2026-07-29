// PredicateBuilder — recursive editor for the composite goal spec
// (gaka-wpb). This is THE novel piece of the goals feature — no
// catalyst-ui primitive to reuse, no existing pattern in the app
// (unlike RemappingForm, which is a flat form).
//
// State-management design (documented up front so a future refactor
// knows the axes it optimized for):
//
//   - LIFT-AND-LOWER via useState at the form root. The outer
//     GoalForm holds `spec: Predicate`; PredicateBuilder takes
//     `{node, onChange}` where onChange replaces the WHOLE spec on
//     any change. Each recursive sub-editor takes its slice of the
//     tree + an onChange that patches its parent. No refs, no
//     mutable state at any level — every edit produces a NEW tree.
//   - Zustand / useReducer were rejected. Zustand would have added
//     ceremony for the modal-scoped ephemeral state (no cross-
//     component subscribers). useReducer would need a discriminated-
//     union action type per predicate variant × per field, which is
//     more lines than the plain onChange plumbing.
//   - Deep copies via structuredClone on every edit. The tree is
//     small (depth cap 5) and React's memo semantics require new
//     references at the path from root to the edited leaf; a full
//     clone is the shortest correct implementation.
//   - Depth propagates via a prop, not context — makes the "should
//     add-group be disabled at depth cap" affordance a pure local
//     check.
//
// UI shape:
//
//   - Group nodes (all/any/not) render as a bordered card with the
//     operator select at the top and indented children below. "Add
//     leaf" appends a fresh time predicate; "Convert to group" wraps
//     the current node in an `all`.
//   - Leaf nodes (time/streak/active_days) render as an inline row
//     of selects + a number field. `time` leaves also expose an
//     axis-value input.
//   - The "remove" affordance on a leaf disappears when the leaf is
//     the only child of a group — the group would end up empty
//     (backend rejects). Convert the group back to a leaf instead.
//
// Round-trip guarantee: what the user builds serializes to JSON that
// passes stats.ValidateSpec and round-trips through the backend
// unchanged. Handler tests + DB tests already cover the wire; this
// component's own tests cover the state transitions.
import { useId, useMemo } from "react";
import { X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/select";
import { MaxPredicateDepth, MaxStreakDays } from "@/features/goals/constants";
import type {
  GoalActiveDaysWindow,
  GoalHeartbeatAxis,
  GoalOp,
  GoalTimeWindow,
  Predicate,
} from "@/types/api";

// ---- axis-value autocomplete ---------------------------------------------
//
// Goal predicates target a specific axis + value (e.g. axis="language",
// value="Go"). The value field used to be a plain <Input> — operators had to
// know the exact string casing/spelling. `AxisValueInput` upgrades that with a
// native <datalist> populated from the user's own recent stats so hitting
// axis=language shows Go/Python/TypeScript/... as suggestions.
//
// Non-existing values are STILL accepted — datalist is suggest-only, not
// restrict-to-list. That's deliberate: an operator can author aspirational
// goals ("learn Rust before I have any Rust time") by just typing.
//
// Data source: /api/v1/users/current/stats windowed to the last 90 days.
// Cached via react-query so opening the modal repeatedly doesn't re-fetch.
// Branch + plugin axes don't come out of /stats — the datalist stays empty
// for those, input still works as free-text.

const AXIS_TO_STATS_KEY: Partial<Record<GoalHeartbeatAxis, keyof StatsAxisMap>> = {
  language: "languages",
  project: "projects",
  editor: "editors",
  category: "categories",
  platform: "platforms",
  machine: "machines",
  // branch + plugin intentionally omitted — /stats doesn't surface them.
};

type StatsAxisMap = {
  languages: ReadonlyArray<{ name: string }>;
  projects: ReadonlyArray<{ name: string }>;
  editors: ReadonlyArray<{ name: string }>;
  categories?: ReadonlyArray<{ name: string }>;
  platforms: ReadonlyArray<{ name: string }>;
  machines: ReadonlyArray<{ name: string }>;
};

function last90(): { start: string; end: string } {
  const end = new Date();
  const start = new Date(end.getTime() - 90 * 24 * 60 * 60 * 1000);
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  return { start: iso(start), end: iso(end) };
}

function AxisValueInput({
  id,
  axis,
  value,
  onChange,
}: {
  id: string;
  axis: GoalHeartbeatAxis;
  value: string;
  onChange: (v: string) => void;
}) {
  const range = useMemo(last90, []);
  const stats = useQuery({
    queryKey: qk.stats(range.start, range.end, undefined, undefined),
    queryFn: () => api.getStats({ start: range.start, end: range.end }),
    staleTime: 5 * 60_000,
  });
  const listId = `${id}-suggestions`;
  const suggestions = useMemo(() => {
    const key = AXIS_TO_STATS_KEY[axis];
    if (!key || !stats.data) return [] as string[];
    const arr = (stats.data as unknown as StatsAxisMap)[key] ?? [];
    return arr
      .map((r) => r.name)
      .filter((n) => typeof n === "string" && n.length > 0)
      // Filter out the display-only "Other (N more)" chart-bounding
      // bucket from stats.capWithOther (internal/stats/segment.go). It
      // isn't a real axis value — a goal predicate against it would
      // never match a heartbeat. Same for a bare literal "Other" name
      // that comes back on some axes when the top-12 cap creates a
      // solo tail. Categories that legitimately are "Other" (the SQL
      // COALESCE(NULL,'Other') path) still match on other queries —
      // this filter only affects the autocomplete surface.
      .filter((n) => !/^Other(\s*\(\d+\s*more\))?$/.test(n))
      .slice(0, 100); // browsers cap datalist rendering; 100 is plenty
  }, [axis, stats.data]);
  return (
    <>
      <Input
        id={id}
        className="h-8"
        value={value}
        placeholder="e.g. Go"
        list={suggestions.length > 0 ? listId : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
      {suggestions.length > 0 && (
        <datalist id={listId}>
          {suggestions.map((s) => (
            <option key={s} value={s} />
          ))}
        </datalist>
      )}
    </>
  );
}

const AXES: GoalHeartbeatAxis[] = [
  "language",
  "project",
  "editor",
  "category",
  "branch",
  "plugin",
  "machine",
  "platform",
];
const TIME_WINDOWS: GoalTimeWindow[] = ["day", "week", "month", "year", "lifetime"];
const ACTIVE_DAYS_WINDOWS: GoalActiveDaysWindow[] = ["week", "month", "year"];
const OPS: GoalOp[] = [">=", "<=", "=="];

// defaultLeaf returns a fresh "time" leaf — the least-surprising
// starting point for a new goal or a new child predicate.
export function defaultLeaf(): Predicate {
  return {
    kind: "time",
    axis: "language",
    value: null,
    op: ">=",
    target_seconds: 3600,
    window: "week",
  };
}

export interface PredicateBuilderProps {
  node: Predicate;
  onChange: (next: Predicate) => void;
  // Depth of this node, 1-based. Root is 1. Passed down so the "add
  // group" and "convert to group" affordances disable at cap.
  depth?: number;
  // Called when the parent wants to remove this node (used by
  // groups to remove a child). undefined at the root — the root is
  // always at least a leaf.
  onRemove?: () => void;
}

export function PredicateBuilder({
  node,
  onChange,
  depth = 1,
  onRemove,
}: PredicateBuilderProps) {
  const canNestDeeper = depth < MaxPredicateDepth;
  switch (node.kind) {
    case "time":
      return (
        <TimeLeafEditor
          node={node}
          onChange={onChange}
          onRemove={onRemove}
          onConvertToGroup={
            canNestDeeper
              ? () => onChange({ kind: "all", of: [node, defaultLeaf()] })
              : undefined
          }
        />
      );
    case "active_days":
      return (
        <ActiveDaysLeafEditor
          node={node}
          onChange={onChange}
          onRemove={onRemove}
        />
      );
    case "streak":
      return (
        <StreakEditor
          node={node}
          onChange={onChange}
          depth={depth}
          onRemove={onRemove}
        />
      );
    case "all":
    case "any":
      return (
        <GroupEditor
          node={node}
          onChange={onChange}
          depth={depth}
          onRemove={onRemove}
        />
      );
    case "not":
      return (
        <NotEditor
          node={node}
          onChange={onChange}
          depth={depth}
          onRemove={onRemove}
        />
      );
    default:
      return null;
  }
}

// --- Kind switcher (allows changing the outer kind of any node) --------
// A group's kind can flip between all / any / not / (leaf); a leaf can
// convert into a group or a streak. The switcher lives at the top of
// every group card (and inline in a leaf's header). Keeping it in one
// place means every conversion is a single dropdown, not scattered
// buttons.
const KIND_LABELS: Record<Predicate["kind"], string> = {
  time: "Time on axis",
  active_days: "Active days",
  streak: "Streak",
  all: "All of (AND)",
  any: "Any of (OR)",
  not: "Not (NOT)",
};

function KindSwitcher({
  current,
  onChange,
  disabledDeepen,
}: {
  current: Predicate["kind"];
  onChange: (nextKind: Predicate["kind"]) => void;
  disabledDeepen: boolean;
}) {
  return (
    <Select value={current} onValueChange={(v: string) => onChange(v as Predicate["kind"])}>
      <SelectTrigger className="w-[180px] h-8 text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="time">{KIND_LABELS.time}</SelectItem>
        <SelectItem value="active_days">{KIND_LABELS.active_days}</SelectItem>
        <SelectItem value="streak" disabled={disabledDeepen}>
          {KIND_LABELS.streak}
        </SelectItem>
        <SelectItem value="all" disabled={disabledDeepen}>
          {KIND_LABELS.all}
        </SelectItem>
        <SelectItem value="any" disabled={disabledDeepen}>
          {KIND_LABELS.any}
        </SelectItem>
        <SelectItem value="not" disabled={disabledDeepen}>
          {KIND_LABELS.not}
        </SelectItem>
      </SelectContent>
    </Select>
  );
}

// convertKind produces a fresh Predicate of `nextKind` seeded by any
// re-usable fields on `from`. Used by every kind switch so the
// conversion logic sits in ONE place.
function convertKind(from: Predicate, nextKind: Predicate["kind"]): Predicate {
  if (from.kind === nextKind) return from;
  switch (nextKind) {
    case "time":
      return defaultLeaf();
    case "active_days":
      return { kind: "active_days", op: ">=", n: 5, window: "week" };
    case "streak":
      return { kind: "streak", min_days: 7, condition: defaultLeaf() };
    case "all":
      return { kind: "all", of: [from.kind === "time" ? from : defaultLeaf()] };
    case "any":
      return { kind: "any", of: [from.kind === "time" ? from : defaultLeaf()] };
    case "not":
      return { kind: "not", of: [from.kind === "time" ? from : defaultLeaf()] };
    default:
      return from;
  }
}

// --- Leaf: time-on-axis ------------------------------------------------
function TimeLeafEditor({
  node,
  onChange,
  onRemove,
  onConvertToGroup,
}: {
  node: Extract<Predicate, { kind: "time" }>;
  onChange: (next: Predicate) => void;
  onRemove?: () => void;
  onConvertToGroup?: () => void;
}) {
  // Stable ids so <Label htmlFor=…> associates each label with its
  // input — RTL's getByLabelText relies on the association, and it's
  // the correct a11y wiring anyway. useId returns per-instance ids
  // that survive re-renders and don't collide with sibling instances.
  const idBase = useId();
  const id = {
    axis: `${idBase}-axis`,
    value: `${idBase}-value`,
    op: `${idBase}-op`,
    target: `${idBase}-target`,
    window: `${idBase}-window`,
  };
  return (
    <div className="rounded-md border bg-secondary/20 p-3">
      <div className="mb-2 flex items-center gap-2">
        <KindSwitcher
          current="time"
          onChange={(k) => onChange(convertKind(node, k))}
          disabledDeepen={!onConvertToGroup}
        />
        <span className="flex-1 text-xs text-muted-foreground">
          Sum time on an axis over a window
        </span>
        {onRemove && (
          <button
            onClick={onRemove}
            title="Remove"
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
        <div>
          <Label htmlFor={id.axis} className="text-xs">Axis</Label>
          <Select
            value={node.axis}
            onValueChange={(v: string) => onChange({ ...node, axis: v as GoalHeartbeatAxis })}
          >
            <SelectTrigger id={id.axis} className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {AXES.map((a) => (
                <SelectItem key={a} value={a}>
                  {a}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div>
          <Label htmlFor={id.value} className="text-xs">Value (blank = any)</Label>
          <AxisValueInput
            id={id.value}
            axis={node.axis}
            value={node.value ?? ""}
            onChange={(v) => onChange({ ...node, value: v === "" ? null : v })}
          />
        </div>
        <div>
          <Label htmlFor={id.op} className="text-xs">Op</Label>
          <Select
            value={node.op}
            onValueChange={(v: string) => onChange({ ...node, op: v as GoalOp })}
          >
            <SelectTrigger id={id.op} className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {OPS.map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div>
          <Label htmlFor={id.target} className="text-xs">Target (seconds)</Label>
          <Input
            id={id.target}
            className="h-8"
            type="number"
            min={0}
            value={node.target_seconds}
            onChange={(e) =>
              onChange({ ...node, target_seconds: Math.max(0, Number(e.target.value) || 0) })
            }
          />
        </div>
        <div className="col-span-2 md:col-span-1">
          <Label htmlFor={id.window} className="text-xs">Window</Label>
          <Select
            value={node.window}
            onValueChange={(v: string) => onChange({ ...node, window: v as GoalTimeWindow })}
          >
            <SelectTrigger id={id.window} className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TIME_WINDOWS.map((w) => (
                <SelectItem key={w} value={w}>
                  {w}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}

// --- Leaf: active_days -------------------------------------------------
function ActiveDaysLeafEditor({
  node,
  onChange,
  onRemove,
}: {
  node: Extract<Predicate, { kind: "active_days" }>;
  onChange: (next: Predicate) => void;
  onRemove?: () => void;
}) {
  const idBase = useId();
  const id = {
    op: `${idBase}-op`,
    n: `${idBase}-n`,
    window: `${idBase}-window`,
  };
  return (
    <div className="rounded-md border bg-secondary/20 p-3">
      <div className="mb-2 flex items-center gap-2">
        <KindSwitcher
          current="active_days"
          onChange={(k) => onChange(convertKind(node, k))}
          disabledDeepen={false}
        />
        <span className="flex-1 text-xs text-muted-foreground">
          Count distinct days with any activity
        </span>
        {onRemove && (
          <button
            onClick={onRemove}
            title="Remove"
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      <div className="grid grid-cols-3 gap-2">
        <div>
          <Label htmlFor={id.op} className="text-xs">Op</Label>
          <Select
            value={node.op}
            onValueChange={(v: string) => onChange({ ...node, op: v as GoalOp })}
          >
            <SelectTrigger id={id.op} className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {OPS.map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div>
          <Label htmlFor={id.n} className="text-xs">N (days)</Label>
          <Input
            id={id.n}
            className="h-8"
            type="number"
            min={0}
            value={node.n}
            onChange={(e) =>
              onChange({ ...node, n: Math.max(0, Number(e.target.value) || 0) })
            }
          />
        </div>
        <div>
          <Label htmlFor={id.window} className="text-xs">Window</Label>
          <Select
            value={node.window}
            onValueChange={(v: string) =>
              onChange({ ...node, window: v as GoalActiveDaysWindow })
            }
          >
            <SelectTrigger id={id.window} className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ACTIVE_DAYS_WINDOWS.map((w) => (
                <SelectItem key={w} value={w}>
                  {w}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}

// --- Streak ------------------------------------------------------------
function StreakEditor({
  node,
  onChange,
  depth,
  onRemove,
}: {
  node: Extract<Predicate, { kind: "streak" }>;
  onChange: (next: Predicate) => void;
  depth: number;
  onRemove?: () => void;
}) {
  const streakMinDaysId = useId();
  return (
    <div className="rounded-md border border-primary/30 bg-secondary/30 p-3">
      <div className="mb-2 flex items-center gap-2">
        <KindSwitcher
          current="streak"
          onChange={(k) => onChange(convertKind(node, k))}
          disabledDeepen={depth >= MaxPredicateDepth}
        />
        <span className="flex-1 text-xs text-muted-foreground">
          Consecutive days meeting the condition (from today, backward)
        </span>
        {onRemove && (
          <button
            onClick={onRemove}
            title="Remove"
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      <div className="mb-3 flex items-end gap-2">
        <div>
          <Label htmlFor={streakMinDaysId} className="text-xs">Min days</Label>
          <Input
            id={streakMinDaysId}
            className="h-8 w-24"
            type="number"
            min={0}
            max={MaxStreakDays}
            value={node.min_days}
            onChange={(e) =>
              onChange({
                ...node,
                min_days: Math.max(0, Math.min(MaxStreakDays, Number(e.target.value) || 0)),
              })
            }
          />
        </div>
        <p className="text-xs text-muted-foreground">
          The condition below is re-evaluated per day; the streak counts hits
          from today backward until a miss.
        </p>
      </div>
      <div className="ml-2 border-l-2 border-primary/30 pl-3">
        <PredicateBuilder
          node={node.condition}
          onChange={(next) => onChange({ ...node, condition: next })}
          depth={depth + 1}
        />
      </div>
    </div>
  );
}

// --- Group (all/any) ---------------------------------------------------
function GroupEditor({
  node,
  onChange,
  depth,
  onRemove,
}: {
  node: Extract<Predicate, { kind: "all" | "any" }>;
  onChange: (next: Predicate) => void;
  depth: number;
  onRemove?: () => void;
}) {
  const canNestDeeper = depth < MaxPredicateDepth;
  return (
    <div className="rounded-md border border-primary/30 bg-secondary/30 p-3">
      <div className="mb-2 flex items-center gap-2">
        <KindSwitcher
          current={node.kind}
          onChange={(k) => onChange(convertKind(node, k))}
          disabledDeepen={false}
        />
        <span className="flex-1 text-xs text-muted-foreground">
          {node.kind === "all"
            ? "All child conditions must hit"
            : "Any child condition hits"}
        </span>
        {onRemove && (
          <button
            onClick={onRemove}
            title="Remove"
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      <div className="ml-2 space-y-2 border-l-2 border-primary/30 pl-3">
        {node.of.map((child, i) => (
          <PredicateBuilder
            key={i}
            node={child}
            depth={depth + 1}
            onChange={(next) => {
              const of = node.of.slice();
              of[i] = next;
              onChange({ ...node, of });
            }}
            onRemove={
              node.of.length > 1
                ? () => {
                    const of = node.of.slice();
                    of.splice(i, 1);
                    onChange({ ...node, of });
                  }
                : undefined
            }
          />
        ))}
      </div>
      <div className="mt-2">
        <Button
          size="sm"
          variant="ghost"
          disabled={!canNestDeeper}
          title={canNestDeeper ? "Add a child condition" : `Max depth ${MaxPredicateDepth} reached`}
          onClick={() =>
            onChange({ ...node, of: [...node.of, defaultLeaf()] })
          }
        >
          + Add condition
        </Button>
      </div>
    </div>
  );
}

// --- Not ---------------------------------------------------------------
function NotEditor({
  node,
  onChange,
  depth,
  onRemove,
}: {
  node: Extract<Predicate, { kind: "not" }>;
  onChange: (next: Predicate) => void;
  depth: number;
  onRemove?: () => void;
}) {
  return (
    <div className="rounded-md border border-primary/30 bg-secondary/30 p-3">
      <div className="mb-2 flex items-center gap-2">
        <KindSwitcher
          current="not"
          onChange={(k) => onChange(convertKind(node, k))}
          disabledDeepen={false}
        />
        <span className="flex-1 text-xs text-muted-foreground">
          Inverts the child (goal is "avoid this")
        </span>
        {onRemove && (
          <button
            onClick={onRemove}
            title="Remove"
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      <div className="ml-2 border-l-2 border-primary/30 pl-3">
        <PredicateBuilder
          node={node.of[0]}
          depth={depth + 1}
          onChange={(next) => onChange({ kind: "not", of: [next] })}
        />
      </div>
    </div>
  );
}
