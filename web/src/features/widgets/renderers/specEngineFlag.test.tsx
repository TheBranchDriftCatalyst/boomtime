// specEngineFlag.test.tsx (Part B Stage 3, gaka-174.x) — proves the
// widgetSpecEngine flag actually routes target:"both" kinds through
// SpecRenderer, and that the flag stays OFF-safe (bespoke switch unchanged)
// in both dispatchers. Non-tautological by construction: the bespoke and
// spec-engine renderers use DIFFERENT data-testids for the same visual
// content (dossier-chip vs spec-chip; a bespoke DeepWorkSessions composite
// vs SpecRenderer's spec-composite), so asserting the right testid shows up
// proves ROUTING, not just "something rendered".
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { WidgetRenderer } from "./WidgetRenderer";
import { OverviewWidgetRenderer } from "./OverviewWidgetRenderer";
import { OverviewDataProvider } from "@/features/overview/OverviewDataContext";
import type { OverviewDataContextValue } from "@/features/overview/OverviewDataContext";
import { renderWithProviders } from "@/test/renderWithProviders";
import { qk } from "@/lib/queryKeys";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { PublicConfig } from "@/types/api";
import type { PublicDashboardPayload, ResourceStats, SessionsPayload } from "@/types/stats";

function stat(name: string, totalSeconds: number): ResourceStats {
  return { name, totalSeconds, totalPct: 0, totalDaily: [], pctDaily: [] };
}

const publicConfig = (widget_spec_engine: boolean): PublicConfig => ({
  registration_enabled: true,
  auth_provider: "local",
  oidc_enabled: false,
  billing_enabled: false,
  beta_flags: {},
  github_connect_enabled: false,
  widget_spec_engine,
});

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

// Renders WidgetRenderer with the flag pre-seeded in the query cache (no
// network round-trip — same technique HeroIdentity.test.tsx uses for
// qk.awards). staleTime:Infinity on usePublicConfig means the seeded value
// is read synchronously on first render.
function renderWidget(kind: string, flag: boolean, view?: string) {
  return renderWithProviders(
    <WidgetRenderer kind={kind} view={view} data={payload()} />,
    {
      queryClient: seededClient(flag),
    },
  );
}

function seededClient(flag: boolean): QueryClient {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, gcTime: 0 } },
  });
  qc.setQueryData(qk.publicConfig(), publicConfig(flag));
  return qc;
}

describe("WidgetRenderer: widgetSpecEngine flag routing", () => {
  it("flag OFF: a both-target kind (editors-chips) renders via the bespoke switch (dossier-chip)", () => {
    renderWidget("editors-chips", false);
    expect(screen.getByTestId("dossier-chip")).toBeInTheDocument();
    expect(screen.queryByTestId("spec-chip")).not.toBeInTheDocument();
  });

  it("flag ON: the SAME both-target kind renders via SpecRenderer (spec-chip)", () => {
    renderWidget("editors-chips", true);
    expect(screen.getByTestId("spec-chip")).toBeInTheDocument();
    expect(screen.queryByTestId("dossier-chip")).not.toBeInTheDocument();
  });

  it("flag ON: an fe-only kind (hero-identity) is UNAFFECTED — still the bespoke hero, never SpecRenderer's no-spec placeholder", () => {
    renderWidget("hero-identity", true);
    expect(screen.getByTestId("hero-identity")).toBeInTheDocument();
    expect(screen.queryByText(/No spec for/)).not.toBeInTheDocument();
  });

  it("flag ON: an unrecognized kind still falls through to WidgetRenderer's own Empty placeholder text (not SpecRenderer's)", () => {
    renderWidget("not-a-real-kind", true);
    expect(screen.getByText('No renderer for "not-a-real-kind"')).toBeInTheDocument();
  });
});

// --- Overview -----------------------------------------------------------
// Overview's target:"both" kinds are self-fetching, so proving the flag
// routes them needs an OverviewDataProvider + the per-hook react-query cache
// pre-seeded (qk.sessions for deep-work — see overviewWidgets.ts). "deep-work"
// is the clearest routing signal: the bespoke path renders DeepWorkSessions
// (a histogram + daily-strip composite, no spec-* testids at all) while the
// spec-engine path renders SpecRenderer's generic multi-panel layout
// (data-testid="spec-composite" + 3 spec-stat-tile metric panels) — see the
// Stage 3 report for why these two are visually DIFFERENT (flagged for
// cutover QA), which is exactly what makes them such a clean routing probe.
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

function renderOverviewWidget(kind: string, flag: boolean) {
  const qc = seededClient(flag);
  qc.setQueryData(qk.sessions(TR.startISO, TR.endISO, TR.timeLimit, undefined), sessionsFixture);
  return renderWithProviders(
    <OverviewDataProvider value={overviewCtx}>
      <OverviewWidgetRenderer kind={kind} />
    </OverviewDataProvider>,
    { queryClient: qc },
  );
}

describe("OverviewWidgetRenderer: widgetSpecEngine flag routing (self-fetch)", () => {
  it("flag OFF: deep-work renders the bespoke DeepWorkSessions composite (no spec-composite testid)", () => {
    renderOverviewWidget("deep-work", false);
    expect(screen.queryByTestId("spec-composite")).not.toBeInTheDocument();
    // Bespoke DeepWorkSessions shows a "Sessions" stat card label too, so
    // pin on its OWN summary numeral instead to prove the bespoke path read
    // the same seeded query (avg length, not shown by the spec-engine path).
    expect(screen.getByText("Avg length")).toBeInTheDocument();
  });

  it("flag ON: deep-work renders SpecRenderer's generic composite (spec-composite, 3 metric tiles)", () => {
    renderOverviewWidget("deep-work", true);
    expect(screen.getByTestId("spec-composite")).toBeInTheDocument();
    expect(screen.getAllByTestId("spec-panel-metric")).toHaveLength(3);
    expect(screen.getByText(String(sessionsFixture.summary.count))).toBeInTheDocument();
    expect(screen.queryByText("Avg length")).not.toBeInTheDocument();
  });

  it("flag ON: an fe-only Overview kind (wellness) is unaffected", () => {
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
    renderOverviewWidget("wellness", true);
    expect(screen.queryByTestId("spec-composite")).not.toBeInTheDocument();
    expect(screen.queryByText(/No spec for/)).not.toBeInTheDocument();
  });
});
