// WidgetRenderer — dispatches a widget kind id to the in-page React
// renderer for the composable dashboard grid (gaka-keb).
//
// Every kind that opts into `dashboardScopes: ['profile']` must have an
// entry here. Unknown / unrendered kinds fall through to a small
// placeholder — the DashboardGrid also filters upstream so this branch
// mostly guards against catalog-renderer drift.
import { Clock, Code, Flame, Trophy, Activity } from "lucide-react";
import { PieChart } from "@/viz/charts/PieChart";
import { Punchcard } from "@/viz/charts/Punchcard";
import { HourBarChart } from "@/viz/charts/HourBarChart";
import { ContributionCalendar } from "@/viz/charts/ContributionCalendar";
import { secondsToCompact, secondsToHms } from "@/lib/utils";
import type { PublicDashboardPayload, ResourceStats } from "@/types/stats";
import {
  computeGrade,
  currentStreak,
  longestStreakInRange,
} from "@/features/publicprofile/grade";
// gaka-wpb: goal tile renderers. Data-fetched internally (batched
// /goals/progress); the outer `data` prop isn't used by these
// kinds. Public dashboard renders (unauth) will 401 silently and
// show the "No goals yet" placeholder — private-by-default.
import { GoalProgress } from "@/features/widgets/renderers/GoalProgress";
import { GoalRing } from "@/features/widgets/renderers/GoalRing";
import { GoalList } from "@/features/widgets/renderers/GoalList";
// gaka-364: label evaluator drives the hero tagline (top-3 awards) +
// the labels-showcase widget. Pure over the payload — but the CATALOG it
// evaluates is now fetched from the DB (gaka-364.3) via useLabelsCatalog.
// While the fetch is in flight `evaluate()` returns [] and the fallback
// "NEW OPERATOR" branch renders — same UX as an actually-empty award set.
import { evaluate } from "@/features/publicprofile/labels/evaluator";
import { useLabelsCatalog } from "@/features/publicprofile/labels/useLabelsCatalog";
import { LabelChip } from "@/features/publicprofile/labels/LabelChip";
import { LabelsShowcase } from "@/features/widgets/renderers/LabelsShowcase";

interface Ctx {
  view?: string;
  width?: number;
  height?: number;
}

export interface WidgetRendererProps {
  kind: string;
  view?: string;
  data: PublicDashboardPayload;
  ctx?: Ctx;
}

export function WidgetRenderer({ kind, view, data, ctx }: WidgetRendererProps) {
  const height = ctx?.height ?? 220;
  switch (kind) {
    case "hero-identity":
      return <HeroIdentity data={data} />;

    case "grade-badge":
      return <GradeBadge data={data} />;

    case "total-time-stat":
      // gaka-k2p: compact `366h 47m` instead of the wrappy
      // `366 hrs 47 mins` — keeps the stat card to a single line so it
      // doesn't grow taller than its row-mates and blow out row-height
      // alignment.
      return (
        <BigStat
          label="TOTAL TIME"
          value={secondsToCompact(data.totalSeconds)}
          Icon={Clock}
        />
      );
    case "daily-avg-stat":
      return (
        <BigStat
          label="DAILY AVG"
          value={secondsToCompact(Math.round(data.dailyAvg))}
          Icon={Code}
        />
      );
    case "current-streak-stat": {
      const n = currentStreak(data.dailyTotal);
      return <BigStat label="CURRENT STREAK" value={`${n}D`} Icon={Flame} />;
    }
    case "longest-streak-stat": {
      const n = longestStreakInRange(data.dailyTotal);
      return <BigStat label="LONGEST STREAK" value={`${n}D`} Icon={Trophy} />;
    }
    case "active-days-stat": {
      const total = data.dailyTotal.length || 1;
      const active = data.dailyTotal.filter((s) => s > 0).length;
      const pct = Math.round((active / total) * 100);
      return (
        <BigStat
          label="ACTIVE DAYS"
          value={`${active}/${total}`}
          sub={`${pct}%`}
          Icon={Activity}
        />
      );
    }

    case "activity-heatmap": {
      const dates = daysFromRange(data.startDate, data.dailyTotal.length);
      return <ContributionCalendar dates={dates} values={data.dailyTotal} />;
    }

    case "top-langs":
      return renderPieOrBar(data.languages, view, height);
    case "top-projects":
      return renderPieOrBar(data.projects, view, height);
    case "categories-chart": {
      if (!data.categories?.length) return <Empty note="No category data yet" />;
      if (view === "pie") return <PieChart items={data.categories} height={height} />;
      return <ChipList items={data.categories} />;
    }

    case "punchcard":
      if (view === "hour-bars") {
        return (
          <HourBarChart
            hour={sumPunchcardByHour(data.punchcard)}
            height={height}
          />
        );
      }
      return <Punchcard data={data.punchcard} height={height} />;

    case "editors-chips":
      return <ChipList items={data.editors} />;
    case "platforms-chips":
      return <ChipList items={data.platforms} />;

    // gaka-wpb goal tiles — self-fetching (see GoalProgress.tsx doc).
    case "goal-progress":
      return <GoalProgress />;
    case "goal-ring":
      return <GoalRing />;
    case "goal-list":
      return <GoalList />;

    // gaka-364: labels showcase — all awarded labels grouped by kind
    case "labels-showcase":
      return <LabelsShowcase data={data} />;

    default:
      return <Empty note={`No renderer for "${kind}"`} />;
  }
}

function renderPieOrBar(items: ResourceStats[], view: string | undefined, height: number) {
  if (!items?.length) return <Empty note="No data" />;
  if (view === "bar") return <BarList items={items.slice(0, 8)} />;
  return <PieChart items={items} height={height} />;
}

function BigStat({
  label,
  value,
  sub,
  Icon,
}: {
  label: string;
  value: string;
  sub?: string;
  Icon?: React.ComponentType<{ size?: number; strokeWidth?: number }>;
}) {
  // gaka-k2p: value uses a fluid clamp() sized against the card width so it
  // never wraps to two lines (which was blowing out the row-height on TOTAL
  // TIME). `dossier-stat__value` picks up the arasaka subtle-glow text-shadow
  // in arasaka.css. `whitespace-nowrap` is the belt-and-braces guarantee
  // against a second line even when the clamp bottoms out.
  return (
    <div
      className="dossier-stat flex h-full w-full flex-col items-start justify-center gap-1 px-3"
      // Container-query context so the value's `cqw`-based font size measures
      // against THIS stat card's width, not the viewport. Keeps 366h 47m fit
      // to a 3-col tile without shrinking a 2-col tile's value into oblivion.
      style={{ containerType: "inline-size" }}
    >
      <div className="dossier-stat__label flex items-center gap-2 text-[10px] uppercase tracking-[0.14em] text-[color:var(--muted-foreground)]">
        {Icon && <Icon size={11} strokeWidth={1.75} />}
        <span>{label}</span>
      </div>
      <div
        className="dossier-stat__value font-mono font-bold leading-none text-[color:var(--primary)] [font-variant-numeric:tabular-nums] whitespace-nowrap overflow-hidden"
        // clamp(min, preferred, max): floor 20px so values always read;
        // preferred is ~3.4% of card width + 12px, so a 200px card ≈ 19px
        // (clamped up to 20) and a 400px card ≈ 26px; max 42px matches the
        // shipped size on wide cards. `cqw` needs the parent
        // `container-type: inline-size` set above.
        style={{ fontSize: "clamp(20px, calc(3.4cqw + 12px), 42px)" }}
        title={value}
      >
        {value}
      </div>
      {sub && (
        <div className="dossier-stat__sub text-xs text-[color:var(--muted-foreground)]">
          {sub}
        </div>
      )}
    </div>
  );
}

function HeroIdentity({ data }: { data: PublicDashboardPayload }) {
  // gaka-364: hero tagline is the top-3 awarded labels from the memeification
  // catalog. Fallback text when there are no awards at all — deliberately
  // unambiguous ("NEW OPERATOR" signals "we've got no data on you" more
  // clearly than the old POLYGLOT-CLASS placeholder ever did).
  //
  // gaka-mem-chip: the previous split of "plain-text tagline row" +
  // "separate emblem row of naked <img>s" collapsed into ONE row of
  // <LabelChip>s. Each chip carries its own image + hover tooltip with
  // the description of what the label means. No duplication.
  const { specs } = useLabelsCatalog();
  const awards = evaluate(data, { catalog: specs });
  const top3 = awards.slice(0, 3);
  return (
    <div className="flex h-full flex-col justify-center px-3">
      <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--muted-foreground)]">
        &gt; PROFILE · {data.username}@boomtime
      </div>
      <div
        className="font-mono text-4xl font-bold uppercase leading-none tracking-tight text-[color:var(--primary)]"
        style={{ textShadow: "0 0 20px var(--primary)" }}
      >
        {data.username}
      </div>
      <div className="mt-2 flex items-center gap-3">
        <span
          className="inline-block h-[2px] w-16 shrink-0"
          style={{ background: "var(--primary)" }}
          aria-hidden
        />
        {top3.length === 0 ? (
          <span
            className="font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--accent,var(--primary))]"
            data-testid="hero-tagline"
          >
            NEW OPERATOR
          </span>
        ) : (
          <div
            className="flex flex-wrap items-center gap-1.5"
            data-testid="hero-tagline"
          >
            {top3.map((a) => (
              <LabelChip key={a.id} award={a} size="sm" />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function GradeBadge({ data }: { data: PublicDashboardPayload }) {
  const grade = computeGrade(data);
  const pctStr = `${Math.round(grade.percentile)}th`;
  return (
    <div className="flex h-full w-full flex-col items-center justify-center gap-1 px-2 text-center">
      <div className="font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--muted-foreground)]">
        &gt; RANK
      </div>
      <div
        className="font-mono text-6xl font-bold leading-none text-[color:var(--primary)]"
        style={{ filter: "drop-shadow(0 0 24px var(--primary))" }}
        data-testid="grade-badge-letter"
      >
        {grade.level}
      </div>
      <div className="font-mono text-[10px] tracking-[0.12em] text-[color:var(--muted-foreground)]">
        {pctStr} PERCENTILE
      </div>
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
              style={{
                background:
                  "linear-gradient(90deg, var(--primary) 0%, var(--primary) 100%)",
                width: `${pct}%`,
                opacity: 0.85,
              }}
              aria-hidden
            />
          </li>
        );
      })}
    </ol>
  );
}

function ChipList({ items }: { items: ResourceStats[] }) {
  if (!items?.length) return <Empty note="No data" />;
  // gaka-k2p: drop the `overflow-y-auto` scroll bar (which was visibly
  // clipping the EDITORS row and rendering a tiny scroll indicator). The
  // grid tile's own `overflow: hidden` still guards catastrophic overflow,
  // but 6-8 chips at h=2 will wrap onto 2 rows without needing a scrollbar.
  // Chips also shrink one step at the base size to leave more room.
  return (
    <div className="dossier-chips flex h-full w-full flex-wrap content-center items-center gap-1.5 px-3 py-2">
      {items.map((it) => (
        <span
          key={it.name}
          className="dossier-chip inline-flex items-center gap-1 rounded-sm border border-[color:var(--primary)]/40 bg-[color:var(--primary)]/10 px-2 py-[3px] font-mono text-[10px] uppercase tracking-[0.08em] whitespace-nowrap"
          style={{ fontSize: Math.max(10, Math.min(14, 10 + (it.totalPct ?? 0) / 10)) + "px" }}
        >
          <span>{it.name}</span>
          <span className="opacity-60">{Math.round(it.totalPct ?? 0)}%</span>
        </span>
      ))}
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
// date strings (YYYY-MM-DD). ContributionCalendar expects parallel
// dates+values arrays; the public payload only carries the start.
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
// collapsing day-of-week. Emits ResourceStats-shaped rows so the existing
// HourBarChart (which expects `{name, totalSeconds}` per hour) renders
// without a bespoke variant.
function sumPunchcardByHour(pc: PublicDashboardPayload["punchcard"]): ResourceStats[] {
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
