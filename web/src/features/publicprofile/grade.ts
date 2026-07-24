// grade.ts — client-side port of internal/stats/grade.go (gaka-hsj port).
//
// Faithful port of github-readme-stats' rank algorithm: same two CDFs, same
// weighted blend, same percentile thresholds and level ladder. The metric
// set matches the Go version so the client-rendered badge on /p/:slug scores
// the same letter grade a widget SVG would.
//
// Kept as a small pure module (no React, no D3) so unit tests hit the
// algorithm directly. If the calibration ever drifts on the backend, add a
// port here and update tests in lockstep.
import type { PublicDashboardPayload } from "@/types/stats";

// Verbatim from internal/stats/grade.go: `gradeLevels` + `gradeThresholds`.
export const GRADE_LEVELS = ["S", "A+", "A", "A-", "B+", "B", "B-", "C+", "C"] as const;
const GRADE_THRESHOLDS = [1, 12.5, 25, 37.5, 50, 62.5, 75, 87.5, 100];

const exponentialCDF = (x: number) => 1 - Math.pow(2, -x);
const logNormalCDF = (x: number) => x / (1 + x);

// DefaultGradeConfig — mirrors internal/stats/grade.go DefaultGradeConfig.
// Kept as a plain export so future recalibration is a one-line diff.
export const DEFAULT_GRADE_CONFIG = {
  streakMedian: 5,
  streakWeight: 2,
  activeMedian: 50,
  activeWeight: 3,
  languagesMedian: 3,
  languagesWeight: 1,
  projectsMedian: 3,
  projectsWeight: 1,
  dailyAvgMedian: 7200, // seconds
  dailyAvgWeight: 4,
  hoursMedian: 40,
  hoursWeight: 1,
  minRangeDays: 7,
};

export interface GradeResult {
  level: (typeof GRADE_LEVELS)[number];
  percentile: number; // 0..100, LOWER is better
  subs: Array<{ metric: string; raw: number; median: number; score: number; weight: number }>;
}

function longestStreak(daily: number[]): number {
  let best = 0;
  let cur = 0;
  for (const s of daily) {
    if (s > 0) {
      cur++;
      if (cur > best) best = cur;
    } else {
      cur = 0;
    }
  }
  return best;
}

// Streak ENDING at "today" (the end of the range). Used by the
// current-streak-stat widget; not part of the grade blend.
export function currentStreak(daily: number[]): number {
  let cur = 0;
  for (let i = daily.length - 1; i >= 0; i--) {
    if (daily[i] > 0) cur++;
    else break;
  }
  return cur;
}

export function longestStreakInRange(daily: number[]): number {
  return longestStreak(daily);
}

// Grade takes a public dashboard payload and returns the same shape the
// backend's stats.Grade returns. `payload` is the minimum useful set of
// fields for the calculation — anything on PublicDashboardPayload works.
//
// The Go version uses `p.LanguagesCount` / `p.ProjectsCount` (true distinct
// counts). PublicDashboardPayload deliberately omits those (they leak
// hidden-value counts on short-list axes). The client-side port falls back
// to `p.languages.length` / `p.projects.length`, which under-reports for
// users with more than the top-N. Acceptable degradation: the top-N is
// deep enough (typically 8-10) that the calibration still produces the
// intended letter for most users, and the leaked-count avoidance is more
// important than a perfectly-aligned client badge.
export function computeGrade(
  payload: Pick<
    PublicDashboardPayload,
    "totalSeconds" | "dailyAvg" | "dailyTotal" | "languages" | "projects"
  >,
  cfg: typeof DEFAULT_GRADE_CONFIG = DEFAULT_GRADE_CONFIG,
): GradeResult {
  const rangeDays = payload.dailyTotal.length;
  let activeDays = 0;
  for (const s of payload.dailyTotal) if (s > 0) activeDays++;
  const denom = Math.max(rangeDays, cfg.minRangeDays);
  const activeRatio = (activeDays / denom) * 100;

  const subs = [
    { metric: "streak", raw: longestStreak(payload.dailyTotal), median: cfg.streakMedian, weight: cfg.streakWeight, score: 0 },
    { metric: "activeDays", raw: activeRatio, median: cfg.activeMedian, weight: cfg.activeWeight, score: 0 },
    { metric: "languages", raw: payload.languages.length, median: cfg.languagesMedian, weight: cfg.languagesWeight, score: 0 },
    { metric: "projects", raw: payload.projects.length, median: cfg.projectsMedian, weight: cfg.projectsWeight, score: 0 },
    { metric: "dailyAvg", raw: payload.dailyAvg, median: cfg.dailyAvgMedian, weight: cfg.dailyAvgWeight, score: 0 },
    { metric: "hours", raw: payload.totalSeconds / 3600, median: cfg.hoursMedian, weight: cfg.hoursWeight, score: 0 },
  ];

  let weightSum = 0;
  let blend = 0;
  for (const s of subs) {
    const x = s.median > 0 ? s.raw / s.median : 0;
    s.score = s.metric === "dailyAvg" || s.metric === "hours" ? logNormalCDF(x) : exponentialCDF(x);
    blend += s.weight * s.score;
    weightSum += s.weight;
  }
  const percentile = weightSum > 0 ? (1 - blend / weightSum) * 100 : 100;
  let level: (typeof GRADE_LEVELS)[number] = GRADE_LEVELS[GRADE_LEVELS.length - 1];
  for (let i = 0; i < GRADE_THRESHOLDS.length; i++) {
    if (percentile <= GRADE_THRESHOLDS[i]) {
      level = GRADE_LEVELS[i];
      break;
    }
  }
  return { level, percentile, subs };
}
