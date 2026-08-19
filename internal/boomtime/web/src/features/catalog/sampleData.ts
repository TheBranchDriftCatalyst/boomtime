// sampleData.ts — deterministic, believable "sample" fixtures for the widget
// catalog gallery (gaka-174.x follow-on). Every one of the 40 catalog kinds
// needs SOMETHING to render for a no-data user and for an unauth visitor of
// a future public catalog page, so this module is the single source of
// truth for "sample" data across every payload shape the renderers consume:
//
//   - SAMPLE_DASHBOARD_PAYLOAD: PublicDashboardPayload — mirrors the shape
//     internal/widget/render_test.go's dataFixture()/payloadFixture() build
//     on the Go side, scaled up to a realistic 90-day window instead of the
//     Go test's tiny 7-day fixture.
//   - The Overview-only payload shapes (StatsPayload, TimelinePayload,
//     PunchcardPayload, SessionsPayload, MomentumPayload, LocPayload,
//     AIActivityPayload, HealthActivityPayload) that the overview-* /
//     ai-assistance / wellness / loc self-fetching FE kinds need.
//   - GithubStatsPayload for the github-* kinds.
//   - Goal + BatchGoalProgress for the goal-* kinds (a couple PUBLIC with
//     partial progress, per the crux requirement).
//   - LabelAward[] + streak map for hero-identity / labels-showcase.
//   - PublicConfig with github_connect_enabled:true so the GitHub-gated
//     kinds actually render instead of self-hiding.
//
// EVERYTHING here is generated ONCE at module load from a seeded PRNG
// (mulberry32) anchored to a FIXED date, not `Date.now()` — so the numbers
// are stable across app loads, test runs, and re-renders (no flaky
// snapshots, no "why did the demo chart just jump" on a page refresh at
// midnight). Values are deliberately non-round (noisy daily series, not
// flat percentages) per the "believable" requirement.
import type {
  AIActivityDay,
  AIActivityPayload,
  HealthActivityDay,
  HealthActivityPayload,
  LocPayload,
  LocPoint,
  LocProject,
  MomentumPayload,
  PublicDashboardPayload,
  PunchcardPayload,
  ResourceStats,
  SessionsPayload,
  StatsPayload,
  TimelinePayload,
  TimelineRange,
} from "@/types/stats";
import type {
  Goal,
  GoalProgress as GoalProgressT,
  BatchGoalProgress,
  PublicConfig,
} from "@/types/api";
import type {
  GithubContributionDay,
  GithubLanguage,
  GithubStatsPayload,
  GithubTopRepo,
} from "@/types/github";
import type { LabelAward } from "@/features/publicprofile/labels/types";
import { DEFAULT_TIME_LIMIT, TIMELINE_HOUR_OPTIONS } from "@/lib/config";

// ---------------------------------------------------------------------------
// Deterministic PRNG — mulberry32. Small, fast, good-enough distribution for
// "believable demo variance", NOT cryptographic. Seeded with a fixed
// constant so every module load produces byte-identical output.
// ---------------------------------------------------------------------------
function mulberry32(seed: number): () => number {
  let s = seed | 0;
  return function next() {
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rng = mulberry32(0xc0ffee42);

// ---------------------------------------------------------------------------
// Fixed time anchor. "Now" for the sample fixtures — NOT wall-clock time —
// so the whole dataset (90-day window, GitHub year grid, timeline lanes) is
// internally consistent and reproducible. Pick a date deep enough into 2026
// that a 365-day GitHub grid never goes negative against any real boot time.
// ---------------------------------------------------------------------------
const DAY_MS = 86_400_000;
export const SAMPLE_NOW = new Date("2026-08-09T20:15:00.000Z");
export const SAMPLE_RANGE_DAYS = 90;
export const SAMPLE_TIME_LIMIT = DEFAULT_TIME_LIMIT;
export const SAMPLE_TIMELINE_HOURS = 24;
export const SAMPLE_USERNAME = "nova.operator";

function dateAtDayOffset(offsetFromStart: number, start: Date): Date {
  return new Date(start.getTime() + offsetFromStart * DAY_MS);
}
function isoDay(d: Date): string {
  return d.toISOString().slice(0, 10);
}

export const SAMPLE_START = new Date(
  SAMPLE_NOW.getTime() - (SAMPLE_RANGE_DAYS - 1) * DAY_MS,
);
// Midnight-normalized start-of-day boundary (matches how StatsPayload's
// StartDate is a day, not a wall-clock instant) — used for date arithmetic.
const SAMPLE_START_DAY = new Date(
  Date.UTC(
    SAMPLE_START.getUTCFullYear(),
    SAMPLE_START.getUTCMonth(),
    SAMPLE_START.getUTCDate(),
  ),
);
export const SAMPLE_START_ISO = SAMPLE_START.toISOString();
export const SAMPLE_END_ISO = SAMPLE_NOW.toISOString();

// ---------------------------------------------------------------------------
// Daily totals — the backbone every resource/heatmap/streak series derives
// from. Weekday-heavy, weekend-light, with rest days and the occasional
// "push" day so streak/grade/heatmap widgets all have real texture.
// ---------------------------------------------------------------------------
const SAMPLE_DAILY_TOTAL: number[] = (() => {
  const out: number[] = [];
  for (let i = 0; i < SAMPLE_RANGE_DAYS; i++) {
    const date = dateAtDayOffset(i, SAMPLE_START_DAY);
    const dow = date.getUTCDay();
    const isWeekend = dow === 0 || dow === 6;
    const base = isWeekend ? 1.15 * 3600 : 5.35 * 3600;
    const variance = (rng() - 0.5) * (isWeekend ? 2.6 * 3600 : 3.2 * 3600);
    let seconds = Math.max(0, base + variance);
    if (rng() < 0.07) seconds = 0; // a rest day
    else if (rng() < 0.12) seconds *= 1.3 + rng() * 0.25; // a push day
    out.push(Math.round(seconds));
  }
  // Guarantee the trailing few days show SOME activity so current-streak
  // widgets have something to report rather than always landing on 0.
  for (let i = SAMPLE_RANGE_DAYS - 4; i < SAMPLE_RANGE_DAYS; i++) {
    if (out[i] === 0) out[i] = Math.round(3600 + rng() * 5400);
  }
  return out;
})();

const SAMPLE_TOTAL_SECONDS = SAMPLE_DAILY_TOTAL.reduce((a, b) => a + b, 0);
const SAMPLE_DAILY_AVG = SAMPLE_TOTAL_SECONDS / SAMPLE_RANGE_DAYS;

// ---------------------------------------------------------------------------
// Resource lists (languages/projects/editors/platforms/categories). Built
// from a target-share list + per-day noise, RESCALED so each day's shares
// sum back to that day's dailyTotal — internally consistent bars/pies, not
// just independently-noisy numbers that don't add up.
// ---------------------------------------------------------------------------
interface ShareSpec {
  name: string;
  share: number;
}

function buildResourceStats(specs: ShareSpec[], dailyTotal: number[]): ResourceStats[] {
  const dailyPerName: number[][] = specs.map(() => new Array(dailyTotal.length).fill(0));
  for (let day = 0; day < dailyTotal.length; day++) {
    const total = dailyTotal[day];
    if (total <= 0) continue;
    const weights = specs.map((s) => s.share * (0.72 + rng() * 0.56));
    const wsum = weights.reduce((a, b) => a + b, 0) || 1;
    for (let idx = 0; idx < specs.length; idx++) {
      dailyPerName[idx][day] = Math.round((weights[idx] / wsum) * total);
    }
  }
  const grand = dailyPerName.reduce((s, arr) => s + arr.reduce((a, b) => a + b, 0), 0) || 1;
  return specs.map((s, idx) => {
    const totalDaily = dailyPerName[idx];
    const totalSeconds = totalDaily.reduce((a, b) => a + b, 0);
    const pctDaily = totalDaily.map((v, day) =>
      dailyTotal[day] > 0 ? Math.round((v / dailyTotal[day]) * 1000) / 10 : 0,
    );
    return {
      name: s.name,
      totalSeconds,
      totalPct: Math.round((totalSeconds / grand) * 1000) / 10,
      totalDaily,
      pctDaily,
    };
  });
}

const PROJECT_SHARES: ShareSpec[] = [
  { name: "boomtime", share: 0.38 },
  { name: "catalyst-ui", share: 0.24 },
  { name: "catalyst-devspace", share: 0.16 },
  { name: "hakboard-dashboard", share: 0.1 },
  { name: "memeX", share: 0.06 },
  { name: "openscad-scripts", share: 0.04 },
  { name: "swarm-graph", share: 0.02 },
];
const LANGUAGE_SHARES: ShareSpec[] = [
  { name: "TypeScript", share: 0.42 },
  { name: "Go", share: 0.22 },
  { name: "Python", share: 0.12 },
  { name: "Rust", share: 0.09 },
  { name: "TSX", share: 0.07 },
  { name: "JSON", share: 0.05 },
  { name: "Markdown", share: 0.03 },
];
const EDITOR_SHARES: ShareSpec[] = [
  { name: "VS Code", share: 0.68 },
  { name: "Neovim", share: 0.22 },
  { name: "JetBrains", share: 0.1 },
];
const PLATFORM_SHARES: ShareSpec[] = [
  { name: "macOS", share: 0.72 },
  { name: "Linux", share: 0.28 },
];
const CATEGORY_SHARES: ShareSpec[] = [
  { name: "coding", share: 0.58 },
  { name: "debugging", share: 0.18 },
  { name: "building", share: 0.14 },
  { name: "code reviewing", share: 0.1 },
];

const SAMPLE_PROJECTS = buildResourceStats(PROJECT_SHARES, SAMPLE_DAILY_TOTAL);
const SAMPLE_LANGUAGES = buildResourceStats(LANGUAGE_SHARES, SAMPLE_DAILY_TOTAL);
const SAMPLE_EDITORS = buildResourceStats(EDITOR_SHARES, SAMPLE_DAILY_TOTAL);
const SAMPLE_PLATFORMS = buildResourceStats(PLATFORM_SHARES, SAMPLE_DAILY_TOTAL);
const SAMPLE_CATEGORIES = buildResourceStats(CATEGORY_SHARES, SAMPLE_DAILY_TOTAL);

// ---------------------------------------------------------------------------
// Punchcard — 7x24 grid with a classic workday shape (morning ramp, lunch
// dip, afternoon peak, evening tail, plus a faint night-owl smear).
// ---------------------------------------------------------------------------
function buildPunchcard(): PunchcardPayload {
  const cells: PunchcardPayload["cells"] = [];
  let maxSeconds = 0;
  let totalSeconds = 0;
  for (let dow = 0; dow < 7; dow++) {
    const isWeekend = dow === 0 || dow === 6;
    for (let hour = 0; hour < 24; hour++) {
      let base: number;
      if (hour >= 9 && hour < 12) base = isWeekend ? 220 : 920;
      else if (hour >= 12 && hour < 13) base = isWeekend ? 140 : 380; // lunch dip
      else if (hour >= 13 && hour < 18) base = isWeekend ? 320 : 1080;
      else if (hour >= 18 && hour < 23) base = isWeekend ? 480 : 520;
      else if (hour >= 0 && hour < 2) base = 160; // night owl tail
      else base = 35;
      const seconds = Math.max(0, Math.round(base * (0.55 + rng() * 0.85)));
      if (seconds > 0) {
        cells.push({ dow, hour, seconds });
        totalSeconds += seconds;
        if (seconds > maxSeconds) maxSeconds = seconds;
      }
    }
  }
  return { cells, maxSeconds, totalSeconds };
}
const SAMPLE_PUNCHCARD = buildPunchcard();

// ---------------------------------------------------------------------------
// Momentum — top-5 projects bucketed into weeks, with boomtime "heating up"
// over the trailing 4 weeks so the momentum grid has a visible hot streak.
// ---------------------------------------------------------------------------
function buildMomentum(): MomentumPayload {
  const weeks: string[] = [];
  const weeklyTotals: number[] = [];
  for (let w = 0; w * 7 < SAMPLE_DAILY_TOTAL.length; w++) {
    weeks.push(isoDay(dateAtDayOffset(w * 7, SAMPLE_START_DAY)));
    weeklyTotals.push(
      SAMPLE_DAILY_TOTAL.slice(w * 7, w * 7 + 7).reduce((a, b) => a + b, 0),
    );
  }
  const top = PROJECT_SHARES.slice(0, 5);
  const projects = top.map((p, idx) => {
    const weekly = weeklyTotals.map((wt, wi) => {
      let v = wt * p.share * (0.65 + rng() * 0.7);
      // boomtime (idx 0) heats up over the trailing 4 weeks.
      if (idx === 0 && wi >= weeklyTotals.length - 4) {
        v *= 1 + (wi - (weeklyTotals.length - 4)) * 0.22;
      }
      return Math.round(v);
    });
    return { name: p.name, weekly, totalSeconds: weekly.reduce((a, b) => a + b, 0) };
  });
  return { weeks, projects };
}
const SAMPLE_MOMENTUM = buildMomentum();

// ---------------------------------------------------------------------------
// Deep-work sessions — 1-3 sessions per active day, split from that day's
// total so summary stats (median/longest) come from real generated data,
// not hand-picked numbers.
// ---------------------------------------------------------------------------
function buildSessions(): SessionsPayload {
  const daily: SessionsPayload["daily"] = [];
  const allLens: number[] = [];
  for (let i = 0; i < SAMPLE_DAILY_TOTAL.length; i++) {
    const date = isoDay(dateAtDayOffset(i, SAMPLE_START_DAY));
    const total = SAMPLE_DAILY_TOTAL[i];
    if (total <= 0) {
      daily.push({ date, sessions: 0, totalSeconds: 0, longestSeconds: 0 });
      continue;
    }
    const count = 1 + Math.floor(rng() * 3);
    let remaining = total;
    const lens: number[] = [];
    for (let s = 0; s < count; s++) {
      const isLast = s === count - 1;
      const chunk = isLast
        ? remaining
        : Math.max(300, Math.round(remaining * (0.3 + rng() * 0.35)));
      lens.push(Math.max(300, chunk));
      remaining -= chunk;
    }
    allLens.push(...lens);
    daily.push({
      date,
      sessions: count,
      totalSeconds: lens.reduce((a, b) => a + b, 0),
      longestSeconds: Math.max(...lens),
    });
  }
  const sorted = [...allLens].sort((a, b) => a - b);
  const summary = {
    count: allLens.length,
    totalSeconds: allLens.reduce((a, b) => a + b, 0),
    avgSeconds: allLens.length
      ? Math.round(allLens.reduce((a, b) => a + b, 0) / allLens.length)
      : 0,
    maxSeconds: allLens.length ? Math.max(...allLens) : 0,
    medianSeconds: sorted.length ? sorted[Math.floor(sorted.length / 2)] : 0,
  };
  const bins = [
    { label: "0-30m", max: 1800 },
    { label: "30-60m", max: 3600 },
    { label: "1-2h", max: 7200 },
    { label: "2-4h", max: 14400 },
    { label: "4h+", max: Infinity },
  ];
  const histogram = bins.map(({ label, max }, i) => {
    const min = i === 0 ? 0 : bins[i - 1].max;
    return { label, count: allLens.filter((l) => l > min && l <= max).length };
  });
  return { summary, daily, histogram };
}
const SAMPLE_SESSIONS = buildSessions();

// ---------------------------------------------------------------------------
// PublicDashboardPayload — the shape the profile page + SpecRenderer + every
// target:"both" widget kind consumes.
// ---------------------------------------------------------------------------
export const SAMPLE_DASHBOARD_PAYLOAD: PublicDashboardPayload = {
  username: SAMPLE_USERNAME,
  startDate: SAMPLE_START_ISO,
  endDate: SAMPLE_END_ISO,
  totalSeconds: SAMPLE_TOTAL_SECONDS,
  dailyAvg: SAMPLE_DAILY_AVG,
  dailyTotal: SAMPLE_DAILY_TOTAL,
  projects: SAMPLE_PROJECTS,
  languages: SAMPLE_LANGUAGES,
  editors: SAMPLE_EDITORS,
  platforms: SAMPLE_PLATFORMS,
  categories: SAMPLE_CATEGORIES,
  punchcard: SAMPLE_PUNCHCARD,
};

export { SAMPLE_MOMENTUM, SAMPLE_SESSIONS };

// StatsPayload — superset PublicDashboardPayload omits (machines, *Count).
// Feeds the Overview self-fetch hooks (useOverviewStats reads this shape).
export const SAMPLE_STATS: StatsPayload = {
  startDate: SAMPLE_START_ISO,
  endDate: SAMPLE_END_ISO,
  totalSeconds: SAMPLE_TOTAL_SECONDS,
  dailyAvg: SAMPLE_DAILY_AVG,
  dailyTotal: SAMPLE_DAILY_TOTAL,
  projects: SAMPLE_PROJECTS,
  languages: SAMPLE_LANGUAGES,
  platforms: SAMPLE_PLATFORMS,
  machines: [{ name: "nova-mbp", totalSeconds: SAMPLE_TOTAL_SECONDS, totalPct: 100, totalDaily: [], pctDaily: [] }],
  editors: SAMPLE_EDITORS,
  categories: SAMPLE_CATEGORIES,
  categoriesCount: SAMPLE_CATEGORIES.length,
  projectsCount: SAMPLE_PROJECTS.length,
  languagesCount: SAMPLE_LANGUAGES.length,
  platformsCount: SAMPLE_PLATFORMS.length,
  machinesCount: 1,
  editorsCount: SAMPLE_EDITORS.length,
};

// ---------------------------------------------------------------------------
// Timeline — one lane set per TIMELINE_HOUR_OPTIONS entry so the "Recent
// timeline" widget's hour-picker never needs a live fetch in sample mode.
// ---------------------------------------------------------------------------
function buildTimeline(hours: number): TimelinePayload {
  const langs: Record<string, TimelineRange[]> = {};
  const names = ["TypeScript", "Go", "Python"];
  let cursor = new Date(SAMPLE_NOW.getTime() - hours * 3_600_000);
  const end = SAMPLE_NOW;
  let li = 0;
  while (cursor < end) {
    const name = names[li % names.length];
    const spanMin = 12 + Math.floor(rng() * 45);
    const rangeStart = new Date(cursor);
    const rangeEnd = new Date(Math.min(cursor.getTime() + spanMin * 60_000, end.getTime()));
    const list = langs[name] ?? (langs[name] = []);
    list.push({ name, rangeStart: rangeStart.toISOString(), rangeEnd: rangeEnd.toISOString() });
    // gap between ranges
    cursor = new Date(rangeEnd.getTime() + Math.floor(rng() * 25) * 60_000);
    li++;
  }
  return { langs };
}
export const SAMPLE_TIMELINE_BY_HOURS: Record<number, TimelinePayload> = Object.fromEntries(
  TIMELINE_HOUR_OPTIONS.map((h) => [h, buildTimeline(h)]),
);

// ---------------------------------------------------------------------------
// Lines of code — total + per-project + a mostly-increasing growth curve.
// ---------------------------------------------------------------------------
function buildLoc(): LocPayload {
  const LOC_SHARES: ShareSpec[] = [
    { name: "boomtime", share: 0.34 },
    { name: "catalyst-ui", share: 0.26 },
    { name: "catalyst-devspace", share: 0.17 },
    { name: "hakboard-dashboard", share: 0.1 },
    { name: "memeX", share: 0.07 },
    { name: "openscad-scripts", share: 0.04 },
    { name: "swarm-graph", share: 0.02 },
  ];
  const totalLoc = 184_320;
  const perProject: LocProject[] = LOC_SHARES.map((s) => ({
    project: s.name,
    loc: Math.round(totalLoc * s.share * (0.9 + rng() * 0.2)),
  }));
  const overTime: LocPoint[] = [];
  let cur = Math.round(totalLoc * 0.55);
  for (let i = 0; i < SAMPLE_RANGE_DAYS; i += 3) {
    cur += Math.round((totalLoc * 0.45 * (3 / SAMPLE_RANGE_DAYS)) * (0.4 + rng() * 1.4));
    overTime.push({ date: isoDay(dateAtDayOffset(i, SAMPLE_START_DAY)), loc: cur });
  }
  overTime[overTime.length - 1].loc = totalLoc;
  return { totalLoc, perProject, overTime };
}
export const SAMPLE_LOC: LocPayload = buildLoc();

// ---------------------------------------------------------------------------
// AI-assistance — populated only on active days, plausible token counts.
// ---------------------------------------------------------------------------
function buildAIActivity(): AIActivityPayload {
  const days: AIActivityDay[] = [];
  let totalIn = 0;
  let totalOut = 0;
  let totalAI = 0;
  let totalHuman = 0;
  let totalSessions = 0;
  let heartbeatsWithAI = 0;
  for (let i = 0; i < SAMPLE_RANGE_DAYS; i++) {
    const day = isoDay(dateAtDayOffset(i, SAMPLE_START_DAY));
    const active = SAMPLE_DAILY_TOTAL[i] > 0;
    if (!active || rng() < 0.35) {
      days.push({ day, aiInputTokens: 0, aiOutputTokens: 0, aiLineChanges: 0, humanLineChanges: 0, aiSessions: 0 });
      continue;
    }
    const aiIn = Math.round(800 + rng() * 4200);
    const aiOut = Math.round(1200 + rng() * 6800);
    const aiLines = Math.round(40 + rng() * 220);
    const humanLines = Math.round(60 + rng() * 340);
    const sessions = 1 + Math.floor(rng() * 3);
    totalIn += aiIn;
    totalOut += aiOut;
    totalAI += aiLines;
    totalHuman += humanLines;
    totalSessions += sessions;
    heartbeatsWithAI += Math.round(10 + rng() * 40);
    days.push({ day, aiInputTokens: aiIn, aiOutputTokens: aiOut, aiLineChanges: aiLines, humanLineChanges: humanLines, aiSessions: sessions });
  }
  return {
    days,
    totalInputTokens: totalIn,
    totalOutputTokens: totalOut,
    totalAILineChanges: totalAI,
    totalHumanLineChanges: totalHuman,
    totalSessions,
    heartbeatsWithAI,
    latestPlan: "Pro",
    hasData: true,
  };
}
export const SAMPLE_AI_ACTIVITY: AIActivityPayload = buildAIActivity();

// ---------------------------------------------------------------------------
// Wellness (Apple Watch / HealthKit) — workouts on ~40% of days, steady
// steps/HR/sleep baselines with day-to-day noise.
// ---------------------------------------------------------------------------
function buildHealthActivity(): HealthActivityPayload {
  const days: HealthActivityDay[] = [];
  const totals: HealthActivityDay = {
    day: "range",
    workouts: 0,
    workoutMinutes: 0,
    activeKcal: 0,
    steps: 0,
    avgHR: 0,
    restingHR: 0,
    sleepMinutes: 0,
    hrvMs: 0,
    mindfulMinutes: 0,
  };
  let hrSampleCount = 0;
  let hrSum = 0;
  let restingSum = 0;
  let hrvSum = 0;
  for (let i = 0; i < SAMPLE_RANGE_DAYS; i++) {
    const day = isoDay(dateAtDayOffset(i, SAMPLE_START_DAY));
    const hasWorkout = rng() < 0.42;
    const workouts = hasWorkout ? 1 : 0;
    const workoutMinutes = hasWorkout ? Math.round(25 + rng() * 55) : 0;
    const activeKcal = Math.round(280 + rng() * 420 + (hasWorkout ? 260 : 0));
    const steps = Math.round(4200 + rng() * 6800);
    const avgHR = Math.round(64 + rng() * 18);
    const restingHR = Math.round(48 + rng() * 8);
    const sleepMinutes = Math.round(370 + rng() * 110);
    const hrvMs = Math.round(38 + rng() * 30);
    const mindfulMinutes = rng() < 0.2 ? Math.round(5 + rng() * 15) : 0;
    days.push({ day, workouts, workoutMinutes, activeKcal, steps, avgHR, restingHR, sleepMinutes, hrvMs, mindfulMinutes });
    totals.workouts += workouts;
    totals.workoutMinutes += workoutMinutes;
    totals.activeKcal += activeKcal;
    totals.steps += steps;
    totals.sleepMinutes += sleepMinutes;
    totals.mindfulMinutes += mindfulMinutes;
    hrSum += avgHR;
    restingSum += restingHR;
    hrvSum += hrvMs;
    hrSampleCount++;
  }
  totals.avgHR = Math.round(hrSum / Math.max(hrSampleCount, 1));
  totals.restingHR = Math.round(restingSum / Math.max(hrSampleCount, 1));
  totals.hrvMs = Math.round(hrvSum / Math.max(hrSampleCount, 1));
  return { hasData: true, days, totals };
}
export const SAMPLE_HEALTH_ACTIVITY: HealthActivityPayload = buildHealthActivity();

// ---------------------------------------------------------------------------
// GitHub stats — trailing ~180-day contribution grid + top repos + language
// bytes. login intentionally differs from the boomtime username (mirrors a
// real "connected GitHub" identity being a different handle).
// ---------------------------------------------------------------------------
function buildGithubStats(): GithubStatsPayload {
  const GH_DAYS = 180;
  const contributionGrid: GithubContributionDay[] = [];
  let commits = 0;
  for (let i = 0; i < GH_DAYS; i++) {
    const date = isoDay(dateAtDayOffset(SAMPLE_RANGE_DAYS - 1 - i, SAMPLE_START_DAY));
    const dow = dateAtDayOffset(SAMPLE_RANGE_DAYS - 1 - i, SAMPLE_START_DAY).getUTCDay();
    const isWeekend = dow === 0 || dow === 6;
    const count = rng() < (isWeekend ? 0.35 : 0.75) ? Math.round(rng() * (isWeekend ? 4 : 9)) : 0;
    contributionGrid.push({ date, count });
    commits += count;
  }
  contributionGrid.reverse();
  const topRepos: GithubTopRepo[] = [
    { name: "boomtime", stars: 341, language: "Go", url: "https://github.com/nova-operator/boomtime" },
    { name: "catalyst-ui", stars: 182, language: "TypeScript", url: "https://github.com/nova-operator/catalyst-ui" },
    { name: "catalyst-devspace", stars: 64, language: "Shell", url: "https://github.com/nova-operator/catalyst-devspace" },
    { name: "openscad-scripts", stars: 21, language: "Python", url: "https://github.com/nova-operator/openscad-scripts" },
    { name: "swarm-graph", stars: 9, language: "Rust", url: "https://github.com/nova-operator/swarm-graph" },
  ];
  const languages: GithubLanguage[] = [
    { name: "TypeScript", bytes: 524_288 },
    { name: "Go", bytes: 312_040 },
    { name: "Python", bytes: 121_800 },
    { name: "Rust", bytes: 54_200 },
    { name: "Shell", bytes: 18_600 },
  ];
  return {
    login: "nova-operator",
    totals: {
      commits,
      pullRequests: 128,
      pullRequestReviews: 96,
      issues: 47,
      repositories: 22,
      restrictedPrivate: 3,
      totalContributions: commits + 128 + 96 + 47,
      followers: 214,
      following: 58,
      stars: topRepos.reduce((s, r) => s + r.stars, 0),
      publicRepos: 22,
      publicGists: 6,
      accountAgeDays: 1642,
    },
    contributionGrid,
    topRepos,
    languages,
    fetchedAt: SAMPLE_NOW.toISOString(),
  };
}
export const SAMPLE_GITHUB_STATS: GithubStatsPayload = buildGithubStats();

// ---------------------------------------------------------------------------
// Goals — a mix of public/private, hit/in-progress, per the "a couple of
// PUBLIC goals with partial progress" requirement.
// ---------------------------------------------------------------------------
export const SAMPLE_GOALS: Goal[] = [
  {
    id: "sample-goal-typescript-weekly",
    owner: SAMPLE_USERNAME,
    name: "TypeScript — 10h/week",
    description: "Keep TypeScript work above 10 hours every week.",
    spec: { kind: "time", axis: "language", value: "TypeScript", op: ">=", target_seconds: 36_000, window: "week" },
    enabled: true,
    public: true,
    createdAt: "2026-05-12T00:00:00Z",
    updatedAt: "2026-08-01T00:00:00Z",
    lastEvaluatedAt: SAMPLE_NOW.toISOString(),
    lastProgress: { hit: false, progress: 0.62, sub_conditions: [] },
  },
  {
    id: "sample-goal-boomtime-monthly",
    owner: SAMPLE_USERNAME,
    name: "Boomtime — 40h/month",
    description: "Ship boomtime features for at least 40 hours a month.",
    spec: { kind: "time", axis: "project", value: "boomtime", op: ">=", target_seconds: 144_000, window: "month" },
    enabled: true,
    public: true,
    createdAt: "2026-04-02T00:00:00Z",
    updatedAt: "2026-08-05T00:00:00Z",
    lastEvaluatedAt: SAMPLE_NOW.toISOString(),
    lastProgress: { hit: false, progress: 0.81, sub_conditions: [] },
  },
  {
    id: "sample-goal-active-days-month",
    owner: SAMPLE_USERNAME,
    name: "20 active days this month",
    description: null,
    spec: { kind: "active_days", op: ">=", n: 20, window: "month" },
    enabled: true,
    public: false,
    createdAt: "2026-03-11T00:00:00Z",
    updatedAt: "2026-08-08T00:00:00Z",
    lastEvaluatedAt: SAMPLE_NOW.toISOString(),
    lastProgress: { hit: true, progress: 1, sub_conditions: [] },
  },
];

export const SAMPLE_GOALS_PROGRESS: BatchGoalProgress = {
  progress: SAMPLE_GOALS.reduce<Record<string, GoalProgressT>>((acc, g) => {
    acc[g.id] = g.lastProgress ?? { hit: false, progress: 0, sub_conditions: [] };
    return acc;
  }, {}),
};

// ---------------------------------------------------------------------------
// Awards / labels — a representative spread across every LabelAward kind so
// labels-showcase renders every group header, and a couple of streak counts
// so LabelChip's "Nx" badge has something to show.
// ---------------------------------------------------------------------------
export const SAMPLE_AWARDS: LabelAward[] = [
  {
    id: "tier-languages-typescript-adept",
    kind: "tier",
    label: "TypeScript Adept",
    glyph: "⟡",
    description: "400+ hours logged in TypeScript.",
    rank: 40,
    tier: "adept",
  },
  {
    id: "archetype-night-owl",
    kind: "archetype",
    label: "Night Owl",
    glyph: "🌙",
    description: "A meaningful share of sessions land after midnight.",
    rank: 55,
  },
  {
    id: "tribe-vscode-loyalist",
    kind: "tribe",
    label: "VS Code Loyalist",
    glyph: "🧩",
    description: "80%+ of sessions run in VS Code.",
    rank: 30,
  },
  {
    id: "meme-sigma-grindset",
    kind: "meme",
    label: "Sigma Grindset",
    glyph: "🗿",
    description: "Absurd day-over-day consistency.",
    rank: 90,
    periodDefault: "weekly",
  },
  {
    id: "patch-rapid-response",
    kind: "patch",
    label: "Rapid Response Team",
    glyph: "★",
    description: "Shipped a fix inside the hour it was reported.",
    rank: 70,
    periodDefault: "daily",
  },
];

export const SAMPLE_AWARD_STREAKS: Record<string, number> = {
  "meme-sigma-grindset": 4,
  "patch-rapid-response": 2,
};

// ---------------------------------------------------------------------------
// Public client config — github_connect_enabled:true so the github-* kinds
// render their charts instead of self-hiding behind the feature gate.
// ---------------------------------------------------------------------------
export const SAMPLE_PUBLIC_CONFIG: PublicConfig = {
  registration_enabled: true,
  auth_provider: "local",
  oidc_enabled: false,
  billing_enabled: false,
  beta_flags: {},
  github_connect_enabled: true,
    books_enabled: false,
};

// ---------------------------------------------------------------------------
// Bundle export — the "OverviewData-shaped" convenience object the task
// calls for. There is no single `OverviewData` payload type in this codebase
// (OverviewDataContextValue only carries the tr/timelineHours/space
// CONTROLS, not fetched data — see OverviewDataContext.tsx); every Overview
// widget instead self-fetches its OWN typed payload via a dedicated hook
// (useOverviewStats -> StatsPayload, useOverviewLoc -> LocPayload, ...). This
// bundle groups all of those payloads under one object so a caller that
// wants "give me everything overview widgets need" doesn't have to import
// nine separate names.
// ---------------------------------------------------------------------------
export const SAMPLE_OVERVIEW_DATA = {
  stats: SAMPLE_STATS,
  timelineByHours: SAMPLE_TIMELINE_BY_HOURS,
  punchcard: SAMPLE_PUNCHCARD,
  sessions: SAMPLE_SESSIONS,
  momentum: SAMPLE_MOMENTUM,
  loc: SAMPLE_LOC,
  aiActivity: SAMPLE_AI_ACTIVITY,
  healthActivity: SAMPLE_HEALTH_ACTIVITY,
  github: SAMPLE_GITHUB_STATS,
  goals: SAMPLE_GOALS,
  goalsProgress: SAMPLE_GOALS_PROGRESS,
  awards: SAMPLE_AWARDS,
  awardStreaks: SAMPLE_AWARD_STREAKS,
  publicConfig: SAMPLE_PUBLIC_CONFIG,
} as const;
