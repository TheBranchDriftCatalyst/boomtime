// specRouting.test.tsx (Part B Stage 5 cutover) — proves target:"both" kinds
// route through SpecRenderer UNCONDITIONALLY and fe-only kinds stay on the
// bespoke switch in both dispatchers. This replaces specEngineFlag.test.tsx
// (deleted at the cutover — the widgetSpecEngine flag it exercised is gone).
// Non-tautological by construction: the bespoke and spec-engine renderers
// use DIFFERENT data-testids for the same visual content (dossier-chip vs
// spec-chip; a bespoke DeepWorkSessions composite vs SpecRenderer's
// spec-composite), so asserting the right testid shows up proves ROUTING,
// not just "something rendered".
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { WidgetRenderer } from "./WidgetRenderer";
import { OverviewWidgetRenderer } from "./OverviewWidgetRenderer";
import { OverviewDataProvider } from "@shared/features/overview/OverviewDataContext";
import type { OverviewDataContextValue } from "@shared/features/overview/OverviewDataContext";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { qk } from "@shared/lib/queryKeys";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import type { PublicDashboardPayload, ResourceStats, SessionsPayload } from "@shared/types/stats";

function stat(name: string, totalSeconds: number): ResourceStats {
  return { name, totalSeconds, totalPct: 0, totalDaily: [], pctDaily: [] };
}

const payload = (over: Partial<PublicDashboardPayload> = {}): PublicDashboardPayload => ({
  username: "pandax",
  startDate: "2026-07-01T00:00:00Z",
  endDate: "2026-07-08T00:00:00Z",
  totalSeconds: 8100,
  dailyAvg: 3600,
  dailyTotal: [3600, 0, 7200],
  projects: [stat("boomtime", 40000)],
  languages: [stat("TypeScript", 30000)],
  editors: [stat("VSCode", 20000)],
  platforms: [stat("macOS", 15000)],
  categories: [stat("Coding", 25000)],
  punchcard: { cells: [], maxSeconds: 0, totalSeconds: 0 },
  ...over,
});

function renderWidget(kind: string, view?: string) {
  return renderWithProviders(<WidgetRenderer kind={kind} view={view} data={payload()} />);
}

describe('WidgetRenderer: target:"both" kinds always route through SpecRenderer', () => {
  it("a both-target kind (editors-chips) renders via SpecRenderer (spec-chip)", () => {
    renderWidget("editors-chips");
    expect(screen.getByTestId("spec-chip")).toBeInTheDocument();
    expect(screen.queryByTestId("dossier-chip")).not.toBeInTheDocument();
  });

  it("an fe-only kind (hero-identity) stays on the bespoke switch — never SpecRenderer's no-spec placeholder", () => {
    renderWidget("hero-identity");
    expect(screen.getByTestId("hero-identity")).toBeInTheDocument();
    expect(screen.queryByText(/No spec for/)).not.toBeInTheDocument();
  });

  it("an unrecognized kind falls through to WidgetRenderer's own Empty placeholder text (not SpecRenderer's)", () => {
    renderWidget("not-a-real-kind");
    expect(screen.getByText('No renderer for "not-a-real-kind"')).toBeInTheDocument();
  });
});

// --- Overview -----------------------------------------------------------
// Overview's target:"both" kinds are self-fetching, so proving the routing
// needs an OverviewDataProvider + the per-hook react-query cache pre-seeded
// (qk.sessions for deep-work — see overviewWidgets.ts). "deep-work" is the
// clearest routing signal: the bespoke path would have rendered
// DeepWorkSessions (a histogram + daily-strip composite, no spec-* testids
// at all) while the spec-engine path renders SpecRenderer's generic
// multi-panel layout (data-testid="spec-composite" + 3 spec-stat-tile
// metric panels) — see the Stage 3 report for why these two are visually
// DIFFERENT, which is exactly what makes them such a clean routing probe.
const TR: OverviewDataContextValue["tr"] = {
  start: new Date("2026-07-01T00:00:00Z"),
  end: new Date("2026-07-08T00:00:00Z"),
  numDays: 7,
  timeLimit: 900,
  startISO: "2026-07-01T00:00:00.000Z",
  endISO: "2026-07-08T00:00:00.000Z",
  setDaysFromToday: () => {},
  setRange: () => {},
  setTimeLimit: () => {},
};

const overviewCtx: OverviewDataContextValue = {
  tr: TR,
  timelineHours: 24,
  setTimelineHours: () => {},
};

const sessionsFixture: SessionsPayload = {
  summary: { count: 4, totalSeconds: 14400, avgSeconds: 3600, maxSeconds: 7200, medianSeconds: 3000 },
  daily: [{ date: "2026-07-01", sessions: 2, totalSeconds: 3600, longestSeconds: 1800 }],
  histogram: [{ label: "0-30m", count: 2 }],
};

function seededClient(): QueryClient {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, gcTime: 0 } },
  });
  qc.setQueryData(qk.sessions(TR.startISO, TR.endISO, TR.timeLimit, undefined), sessionsFixture);
  return qc;
}

function renderOverviewWidget(kind: string) {
  return renderWithProviders(
    <OverviewDataProvider value={overviewCtx}>
      <OverviewWidgetRenderer kind={kind} />
    </OverviewDataProvider>,
    { queryClient: seededClient() },
  );
}

describe('OverviewWidgetRenderer: target:"both" kinds always route through SpecRenderer (self-fetch)', () => {
  it("deep-work renders SpecRenderer's generic composite (spec-composite, 3 metric tiles)", () => {
    renderOverviewWidget("deep-work");
    expect(screen.getByTestId("spec-composite")).toBeInTheDocument();
    expect(screen.getAllByTestId("spec-panel-metric")).toHaveLength(3);
    expect(screen.getByText(String(sessionsFixture.summary.count))).toBeInTheDocument();
  });

  it("an fe-only Overview kind (wellness) is unaffected", () => {
    // WellnessCard self-fetches health activity; mock it explicitly (empty/
    // no-data, matching its self-hide contract) rather than leaving the
    // request unhandled.
    server.use(
      http.get("/api/v1/users/current/stats/health", () =>
        HttpResponse.json({
          hasData: false,
          days: [],
          totals: {
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
          },
        }),
      ),
    );
    renderOverviewWidget("wellness");
    expect(screen.queryByTestId("spec-composite")).not.toBeInTheDocument();
    expect(screen.queryByText(/No spec for/)).not.toBeInTheDocument();
  });
});
