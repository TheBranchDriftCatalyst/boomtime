// SpecRenderer (Part B Stage 3, gaka-174.x) — the FE twin of
// internal/widget/spec.go's renderSpec/renderSpecPanel. Looks up a kind's
// canonical spec (specs.ts, the SAME internal/widget/specs.json the backend
// embeds) and dispatches each panel's `primitive` to a FE viz component via
// PRIMITIVE_REGISTRY (a map, not a switch — see the guard test in
// SpecRenderer.test.tsx that every "both" kind's primitives all have an
// entry). `binding` resolution mirrors spec.go's resolveResources /
// resolveSeries / metricLabelValue / statNumeralLabelValue functions
// exactly, just reading off SpecRenderData instead of *widget.Data.
//
// This is the data-driven alternative to WidgetRenderer.tsx's/
// OverviewWidgetRenderer.tsx's hand-written `switch(kind)` cases for
// target:"both" kinds. It is wired in behind the widgetSpecEngine FE flag
// (sourced from BOOM_WIDGET_SPEC_ENGINE via /api/v1/config/public) — see
// those two files for the delegation. fe-only kinds never reach here; both
// dispatchers keep their bespoke case for those regardless of the flag.
//
// Multi-panel ("composite") specs (stats-card, stats-card-with-grade,
// profile-summary, deep-work) render every panel in a small flex layout —
// unlike the backend's pixel-exact `rect` placement on an SVG canvas, this
// is an in-page DOM render, so exact rect geometry doesn't carry over (see
// the package doc on specs.json's panelRect for why the backend needs it and
// this file doesn't).
import { PieChart } from "@/viz/charts/PieChart";
import { Punchcard } from "@/viz/charts/Punchcard";
import { HourBarChart } from "@/viz/charts/HourBarChart";
import { ContributionCalendar } from "@/viz/charts/ContributionCalendar";
import { CumulativeArea } from "@/viz/charts/CumulativeArea";
import { HeatmapChart } from "@/viz/charts/HeatmapChart";
import { MomentumGrid } from "@/viz/charts/MomentumGrid";
import { secondsToCompact, secondsToHms } from "@/lib/utils";
import {
  computeGrade,
  currentStreak,
  longestStreakInRange,
} from "@/features/publicprofile/grade";
import type {
  ResourceStats,
  PunchcardPayload,
  MomentumPayload,
  SessionsPayload,
} from "@/types/stats";
import { specForKind, type SpecPanel } from "@/features/widgets/specs";

// The subset of PublicDashboardPayload (+ the Overview-only punchcard/
// momentum/sessions payloads, which live outside PublicDashboardPayload)
// every "both" kind's bindings can resolve against. PublicDashboardPayload
// satisfies this structurally as-is (momentum/sessions are optional — the
// public profile payload never carries them; only Overview's self-fetched
// composite object does, see OverviewWidgetRenderer.tsx).
export interface SpecRenderData {
  totalSeconds: number;
  dailyAvg: number;
  dailyTotal: number[];
  startDate: string;
  projects: ResourceStats[];
  languages: ResourceStats[];
  editors: ResourceStats[];
  platforms: ResourceStats[];
  categories: ResourceStats[];
  punchcard: PunchcardPayload;
  // Overview-only bindings — absent on the public profile payload. Panels
  // that need them (momentum kind, deep-work's metric/area panels) degrade
  // to an empty state rather than throwing when unset.
  momentum?: MomentumPayload;
  sessions?: SessionsPayload;
}

export interface SpecRendererProps {
  kind: string;
  view?: string;
  data: SpecRenderData;
  height?: number;
}

/** Looks up `kind`'s spec and renders its panel(s). Renders a placeholder
 * for an unknown kind or a fe-only kind (callers should have already routed
 * fe-only kinds to their bespoke renderer — see WidgetRenderer.tsx). */
export function SpecRenderer({ kind, view, data, height = 220 }: SpecRendererProps) {
  const spec = specForKind(kind);
  if (!spec || spec.target !== "both" || !spec.panels?.length) {
    return <Empty note={`No spec for "${kind}"`} />;
  }
  if (spec.panels.length === 1) {
    return <Panel panel={spec.panels[0]} data={data} view={view} height={height} />;
  }
  // Composite: lay every panel out in a wrapping flex row rather than
  // reproducing the backend's exact `rect` geometry (see file doc).
  return (
    <div className="flex h-full w-full flex-wrap items-stretch gap-2 p-1" data-testid="spec-composite">
      {spec.panels.map((panel, i) => (
        <div
          key={`${panel.primitive}-${panel.binding}-${panel.field ?? i}`}
          className="min-w-[110px] flex-1 basis-[45%]"
          data-testid={`spec-panel-${panel.primitive}`}
        >
          <Panel panel={panel} data={data} view={view} height={Math.max(90, Math.floor(height / 2))} />
        </div>
      ))}
    </div>
  );
}

interface PrimitiveProps {
  panel: SpecPanel;
  data: SpecRenderData;
  view?: string;
  height: number;
}

function Panel(props: PrimitiveProps) {
  const Component = PRIMITIVE_REGISTRY[props.panel.primitive];
  if (!Component) {
    return <Empty note={`Unsupported primitive "${props.panel.primitive}"`} />;
  }
  return <Component {...props} />;
}

// ---------------------------------------------------------------------------
// primitive -> component registry. A map, not a switch, so
// SpecRenderer.test.tsx can assert every primitive named in a "both" spec
// has an entry (no kind can silently fall through to "unsupported").
// ---------------------------------------------------------------------------

const PRIMITIVE_REGISTRY: Record<string, (props: PrimitiveProps) => React.ReactElement> = {
  bars: BarsPrimitive,
  chips: ChipsPrimitive,
  calendar: CalendarPrimitive,
  area: AreaPrimitive,
  "day-heatmap": DayHeatmapPrimitive,
  punchcard: PunchcardPrimitive,
  momentum: MomentumPrimitive,
  "grade-ring": GradeRingPrimitive,
  metric: MetricPrimitive,
  "stat-numeral": StatNumeralPrimitive,
  ratio: RatioPrimitive,
  badge: BadgePrimitive,
};

/** The primitive vocabulary this file supports — exported so
 * SpecRenderer.test.tsx's guard test can diff it against every primitive
 * named across specs.json's "both" kinds. */
export const SUPPORTED_PRIMITIVES = new Set(Object.keys(PRIMITIVE_REGISTRY));

// ---------------------------------------------------------------------------
// Binding resolvers — mirror internal/widget/spec.go's resolveResources /
// resolveSeries / metricLabelValue / statNumeralLabelValue.
// ---------------------------------------------------------------------------

// "machines" is a valid binding in the vocabulary but PublicDashboardPayload
// deliberately omits a machines segment (identifying data), and no "both"
// spec kind actually binds a panel to it today — resolveResources on the Go
// side would error on an unhandled binding; here an empty list is the safer
// FE degrade (no kind currently exercises this path).
function resolveResources(data: SpecRenderData, binding: string): ResourceStats[] {
  switch (binding) {
    case "languages":
      return data.languages;
    case "projects":
      return data.projects;
    case "platforms":
      return data.platforms;
    case "editors":
      return data.editors;
    case "categories":
      return data.categories;
    default:
      return [];
  }
}

function BarsPrimitive({ panel, data, view, height }: PrimitiveProps) {
  const items = resolveResources(data, panel.binding);
  if (!items?.length) return <Empty note="No data" />;
  if (view === "bar") return <BarList items={items.slice(0, 8)} />;
  return <PieChart items={items} height={height} />;
}

function ChipsPrimitive({ panel, data, view, height }: PrimitiveProps) {
  const items = resolveResources(data, panel.binding);
  if (!items?.length) return <Empty note="No data" />;
  if (view === "pie") return <PieChart items={items} height={height} />;
  return <Chips items={items} />;
}

function CalendarPrimitive({ data }: PrimitiveProps) {
  const dates = daysFromRange(data.startDate, data.dailyTotal.length);
  return <ContributionCalendar dates={dates} values={data.dailyTotal} />;
}

// "area" is bound to either "daily-total" (cumulative-area) or "sessions"
// (deep-work's chart panel — the daily session-time shape, same axis as
// dailyTotal per spec.go's resolveSeries doc comment).
function AreaPrimitive({ panel, data, height }: PrimitiveProps) {
  if (panel.binding === "sessions") {
    const daily = data.sessions?.daily ?? [];
    if (!daily.length) return <Empty note="No session data yet" />;
    return (
      <CumulativeArea
        dates={daily.map((d) => d.date)}
        values={daily.map((d) => d.totalSeconds)}
        height={height}
      />
    );
  }
  const dates = daysFromRange(data.startDate, data.dailyTotal.length);
  return <CumulativeArea dates={dates} values={data.dailyTotal} height={height} />;
}

function DayHeatmapPrimitive({ panel, data, height }: PrimitiveProps) {
  const items = resolveResources(data, panel.binding);
  const dates = daysFromRange(data.startDate, data.dailyTotal.length);
  return <HeatmapChart items={items} dates={dates} height={height} />;
}

function PunchcardPrimitive({ data, view, height }: PrimitiveProps) {
  if (view === "hour-bars") {
    return <HourBarChart hour={sumPunchcardByHour(data.punchcard)} height={height} />;
  }
  return <Punchcard data={data.punchcard} height={height} />;
}

function MomentumPrimitive({ data }: PrimitiveProps) {
  return <MomentumGrid data={data.momentum} />;
}

function GradeRingPrimitive({ data }: PrimitiveProps) {
  const grade = computeGrade(data);
  const r = 30;
  const size = r * 2 + 12;
  const circ = 2 * Math.PI * r;
  const fillLen = (circ * (100 - grade.percentile)) / 100;
  return (
    <div className="flex h-full w-full items-center justify-center" data-testid="spec-grade-ring">
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label={`Grade ${grade.level}`}>
        <circle cx={size / 2} cy={size / 2} r={r} stroke="var(--border)" strokeWidth={6} fill="none" />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          stroke="var(--primary)"
          strokeWidth={6}
          fill="none"
          strokeLinecap="round"
          strokeDasharray={`${fillLen} ${circ}`}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
        />
        <text
          x={size / 2}
          y={size / 2}
          textAnchor="middle"
          dominantBaseline="central"
          fontSize={20}
          fontWeight={700}
          fill="var(--primary)"
          data-testid="spec-grade-ring-letter"
        >
          {grade.level}
        </text>
      </svg>
    </div>
  );
}

// binding -> (label, value) for the "metric" primitive. Mirrors spec.go's
// metricLabelValue: scalar bindings read straight off the payload; the
// "sessions" binding needs `field` to pick which summary number to show.
function metricLabelValue(data: SpecRenderData, panel: SpecPanel): [string, string] {
  switch (panel.binding) {
    case "total-seconds":
      return [panel.title ?? "Total", secondsToCompact(data.totalSeconds)];
    case "daily-avg":
      return [panel.title ?? "Daily avg", secondsToCompact(Math.round(data.dailyAvg))];
    case "sessions": {
      const summary = data.sessions?.summary;
      switch (panel.field) {
        case "median":
          return [panel.title ?? "Median length", secondsToCompact(summary?.medianSeconds ?? 0)];
        case "longest":
          return [panel.title ?? "Longest", secondsToCompact(summary?.maxSeconds ?? 0)];
        default:
          return [panel.title ?? "Sessions", String(summary?.count ?? 0)];
      }
    }
    default:
      return [panel.title ?? panel.binding, "—"];
  }
}

function MetricPrimitive({ panel, data }: PrimitiveProps) {
  const [label, value] = metricLabelValue(data, panel);
  return <StatTile label={label} value={value} compact />;
}

// binding -> (label, value) for the "stat-numeral" primitive (the
// standalone big-numeral tiles). Mirrors spec.go's statNumeralLabelValue.
function statNumeralLabelValue(data: SpecRenderData, panel: SpecPanel): [string, string] {
  switch (panel.binding) {
    case "total-seconds":
      return [panel.title ?? "TOTAL TIME", secondsToCompact(data.totalSeconds)];
    case "daily-avg":
      return [panel.title ?? "DAILY AVG", secondsToCompact(Math.round(data.dailyAvg))];
    case "streak-current":
      return [panel.title ?? "CURRENT STREAK", `${currentStreak(data.dailyTotal)}D`];
    case "streak-longest":
      return [panel.title ?? "LONGEST STREAK", `${longestStreakInRange(data.dailyTotal)}D`];
    default:
      return [panel.title ?? panel.binding, "—"];
  }
}

function StatNumeralPrimitive({ panel, data }: PrimitiveProps) {
  const [label, value] = statNumeralLabelValue(data, panel);
  return <StatTile label={label} value={value} />;
}

// "ratio" is always the active-days binding — mirrors WidgetRenderer.tsx's
// active-days-stat case / spec.go's stats.ActiveDays (denom floors to 1).
function RatioPrimitive({ data }: PrimitiveProps) {
  const total = data.dailyTotal.length || 1;
  const active = data.dailyTotal.filter((s) => s > 0).length;
  const pct = Math.round((active / total) * 100);
  return <StatTile label="ACTIVE DAYS" value={`${active}/${total}`} sub={`${pct}%`} />;
}

// Badge is SVG-embed-only (renderSpec special-cases it to a shields.io-style
// pill rather than a card panel — see spec.go's doc comment). It's never
// reached in-page today (no dashboardScopes offer the "badge" kind), but the
// registry still needs an entry so the guard test passes and a future caller
// gets a reasonable pill rather than "unsupported primitive".
function BadgePrimitive({ data }: PrimitiveProps) {
  return (
    <div className="flex h-full w-full items-center justify-center" data-testid="spec-badge">
      <span className="inline-flex items-center gap-1 rounded-sm border border-[color:var(--primary)]/40 bg-[color:var(--primary)]/10 px-2 py-[3px] font-mono text-[10px] uppercase tracking-[0.08em] whitespace-nowrap">
        boomtime: {secondsToCompact(data.totalSeconds)}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Small shared visuals — local, not exported. Deliberately parallel
// (slightly restyled for composite-panel scale) rather than importing
// WidgetRenderer.tsx's private BigStat/BarList/ChipList, matching this
// codebase's existing convention of small per-file local helpers (see e.g.
// sumPunchcardByHour, duplicated identically in WidgetRenderer.tsx and
// OverviewWidgetRenderer.tsx).
// ---------------------------------------------------------------------------

function StatTile({
  label,
  value,
  sub,
  compact,
}: {
  label: string;
  value: string;
  sub?: string;
  compact?: boolean;
}) {
  return (
    <div
      className="flex h-full w-full flex-col items-start justify-center gap-1 px-2"
      data-testid="spec-stat-tile"
    >
      <div className="font-mono text-[10px] uppercase tracking-[0.14em] text-[color:var(--muted-foreground)]">
        {label}
      </div>
      <div
        className={
          (compact ? "text-base" : "text-2xl") +
          " font-mono font-bold leading-none text-[color:var(--primary)] [font-variant-numeric:tabular-nums]"
        }
      >
        {value}
      </div>
      {sub && (
        <div className="text-xs text-[color:var(--muted-foreground)]">{sub}</div>
      )}
    </div>
  );
}

function BarList({ items }: { items: ResourceStats[] }) {
  const max = Math.max(...items.map((i) => i.totalSeconds), 1);
  return (
    <ol className="flex h-full w-full flex-col gap-1 overflow-y-auto px-2 py-1">
      {items.map((it) => {
        const pct = (it.totalSeconds / max) * 100;
        return (
          <li key={it.name} className="flex flex-col gap-0.5">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.1em] text-[color:var(--muted-foreground)]">
              <span className="truncate">{it.name}</span>
              <span>{secondsToHms(it.totalSeconds)}</span>
            </div>
            <div
              className="h-[6px] rounded-sm"
              style={{ background: "var(--primary)", width: `${pct}%`, opacity: 0.85 }}
              aria-hidden
            />
          </li>
        );
      })}
    </ol>
  );
}

function Chips({ items }: { items: ResourceStats[] }) {
  const grand = items.reduce((s, it) => s + (it.totalSeconds || 0), 0) || 1;
  return (
    <div
      className="flex h-full w-full flex-wrap content-center items-center gap-1.5 px-3 py-2"
      data-testid="spec-chips"
    >
      {items.map((it) => {
        const pct = (it.totalSeconds / grand) * 100;
        return (
          <span
            key={it.name}
            data-testid="spec-chip"
            className="inline-flex items-center gap-1 rounded-sm border border-[color:var(--primary)]/40 bg-[color:var(--primary)]/10 px-2 py-[3px] font-mono text-[10px] uppercase tracking-[0.08em] whitespace-nowrap"
          >
            <span>{it.name}</span>
            <span className="opacity-60">{Math.round(pct)}%</span>
          </span>
        );
      })}
    </div>
  );
}

function Empty({ note }: { note: string }) {
  return (
    <div className="flex h-full w-full items-center justify-center font-mono text-[11px] uppercase tracking-[0.15em] text-[color:var(--muted-foreground)]">
      {note}
    </div>
  );
}

// Given an ISO start-date and a length N, produce N consecutive daily
// date strings (YYYY-MM-DD). Duplicated from WidgetRenderer.tsx (private,
// unexported there) — see the file-doc note on local-helper duplication.
function daysFromRange(startISO: string, count: number): string[] {
  const out: string[] = [];
  const start = new Date(startISO);
  for (let i = 0; i < count; i++) {
    const d = new Date(start);
    d.setUTCDate(start.getUTCDate() + i);
    out.push(d.toISOString().slice(0, 10));
  }
  return out;
}

// Sum a punchcard's 7x24 grid down to a single hour-of-day 24-bin series,
// collapsing day-of-week. Duplicated from WidgetRenderer.tsx /
// OverviewWidgetRenderer.tsx (see the file-doc note on local-helper
// duplication) — emits ResourceStats-shaped rows so HourBarChart renders
// without a bespoke variant.
function sumPunchcardByHour(pc: PunchcardPayload): ResourceStats[] {
  const totals = new Array<number>(24).fill(0);
  for (const c of pc.cells) {
    if (c.hour >= 0 && c.hour < 24) totals[c.hour] += c.seconds;
  }
  const grand = totals.reduce((s, v) => s + v, 0) || 1;
  return totals.map((total, h) => ({
    name: String(h),
    totalSeconds: total,
    totalPct: (total / grand) * 100,
    totalDaily: [],
    pctDaily: [],
  }));
}
