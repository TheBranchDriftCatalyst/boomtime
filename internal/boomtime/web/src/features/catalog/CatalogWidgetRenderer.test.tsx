// CatalogWidgetRenderer.test.tsx — the CRUX invariant under test: every
// self-fetching FE kind renders POPULATED content under source="sample"
// with ZERO network calls. MSW is configured (src/test/setup.ts) to error
// on any unhandled request, and none of these tests stub a single route —
// if CatalogDataSource.tsx's seeding ever misses a query key a kind below
// actually needs, this file fails loudly with an MSW "unhandled request"
// error pointing at the exact missing endpoint, instead of a silent network
// call slipping through in production.
//
// Coverage spans every dispatch path CatalogWidgetRenderer.tsx relies on:
//   - "both" kinds fed synchronously via WidgetRenderer -> SpecRenderer
//     (stats-card: composite; top-langs: single-panel; punchcard: needs the
//     payload's punchcard field; cumulative-area: needs dailyTotal).
//   - goal-* "both" kinds, which are SELF-FETCHING inside SpecRenderer
//     (GoalProgress/GoalRing/GoalList ignore the `data` prop and read
//     useGoalsQuery/useAllGoalProgress from the seeded QueryClient).
//   - fe-only PROFILE kinds routed via WidgetRenderer's own switch
//     (grade-badge: data-prop only, no query at all; hero-identity +
//     labels-showcase: self-fetch awards/streaks; github-stats:
//     self-fetches usePublicConfig() + the public GH mirror by slug).
//   - fe-only OVERVIEW kinds routed via OverviewWidgetRenderer, self-
//     fetching from the seeded OverviewDataContext (overview-stats, loc,
//     github-commits, ai-assistance, wellness, overview-timeline).
import { beforeEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { CatalogWidgetRenderer } from "./CatalogWidgetRenderer";
import { SAMPLE_GOALS, SAMPLE_USERNAME } from "./sampleData";
import { __resetReadingRange } from "@/features/overview/reading/readingRange";

// >=2 "both", >=2 fe-only (incl. one overview self-fetcher), >=1 goal, plus
// extra coverage across every category so a regression in any one dispatch
// branch fails here rather than silently in the real gallery.
const REPRESENTATIVE_KINDS = [
  // both — synchronous, data-prop only
  "stats-card",
  "top-langs",
  "punchcard",
  "cumulative-area",
  // both, self-fetching (goal-*)
  "goal-progress",
  "goal-ring",
  "goal-list",
  // fe-only, profile-scoped (WidgetRenderer's switch)
  "grade-badge",
  "hero-identity",
  "labels-showcase",
  "github-stats",
  // fe-only, OVERVIEW-scoped self-fetchers (OverviewWidgetRenderer)
  "overview-stats",
  "loc",
  "github-commits",
  "ai-assistance",
  "wellness",
  "overview-timeline",
];

function renderSample(kind: string) {
  return renderWithProviders(<CatalogWidgetRenderer kind={kind} source="sample" />, {
    withRouter: true,
  });
}

describe('CatalogWidgetRenderer(source="sample") — every representative kind renders, no network', () => {
  it.each(REPRESENTATIVE_KINDS)("%s: renders non-empty output, no fallback placeholder", async (kind) => {
    const { container } = renderSample(kind);
    await waitFor(() => {
      expect(container.innerHTML.trim().length).toBeGreaterThan(0);
    });
    // Never falls through to a "not wired" placeholder — the strongest
    // available correctness signal (a kind that quietly hits the fallback
    // still produces "non-empty output" by the innerHTML check alone).
    expect(container.textContent).not.toMatch(
      /No spec for|Unsupported primitive|No renderer for|No spec renderer for/,
    );
  });
});

describe('CatalogWidgetRenderer(source="sample") — targeted content assertions', () => {
  it("stats-card shows the sample total-time metric panel", async () => {
    // The "bars/languages" panel renders via PieChart (a D3 surface that
    // measures 0 width in jsdom and skips its draw — see SpecRenderer.test's
    // own note on this); the "metric" panels are plain DOM and always
    // assertable, so they carry the content-correctness weight here.
    renderSample("stats-card");
    await waitFor(() => expect(screen.getByText("Total")).toBeInTheDocument());
    expect(screen.getByTestId("spec-panel-bars")).toBeInTheDocument();
  });

  it("grade-badge shows a computed letter grade", async () => {
    renderSample("grade-badge");
    await waitFor(() => {
      const el = screen.getByTestId("grade-badge-letter");
      expect(el.textContent?.trim().length ?? 0).toBeGreaterThan(0);
    });
  });

  it("hero-identity shows the sample username", async () => {
    renderSample("hero-identity");
    await waitFor(() => expect(screen.getByText(SAMPLE_USERNAME)).toBeInTheDocument());
  });

  it("labels-showcase renders seeded awards, not the empty-state placeholder", async () => {
    renderSample("labels-showcase");
    await waitFor(() => {
      expect(screen.queryByText(/NO LABELS YET/)).not.toBeInTheDocument();
    });
  });

  it("goal-progress shows the first enabled sample goal's name", async () => {
    renderSample("goal-progress");
    await waitFor(() => expect(screen.getByText(SAMPLE_GOALS[0].name)).toBeInTheDocument());
  });

  it("goal-list shows every enabled sample goal", async () => {
    renderSample("goal-list");
    for (const g of SAMPLE_GOALS.filter((x) => x.enabled)) {
      await waitFor(() => expect(screen.getByText(g.name)).toBeInTheDocument());
    }
  });

  it("github-stats (public GH mirror self-fetch) renders the GitHub Activity header", async () => {
    renderSample("github-stats");
    await waitFor(() => expect(screen.getByTestId("github-card-header")).toBeInTheDocument());
  });

  it("overview-stats (OverviewWidgetRenderer self-fetch) shows the stat strip", async () => {
    renderSample("overview-stats");
    await waitFor(() => expect(screen.getByText(/Total tracked time/i)).toBeInTheDocument());
  });

  it("loc (overview self-fetch) shows the Lines of code headline", async () => {
    renderSample("loc");
    await waitFor(() => expect(screen.getByText(/Lines of code/i)).toBeInTheDocument());
  });

  it("overview-timeline shows the hour-picker with the seeded default window", async () => {
    renderSample("overview-timeline");
    await waitFor(() => expect(screen.getByText(/Last \d+ hours/)).toBeInTheDocument());
  });
});

// ---------------------------------------------------------------------------
// gaka-qcxg — reading-domain kinds. These are dispatched (by CatalogWidget
// Renderer's READING_KINDS map) to the existing Reading dashboard tiles, which
// SELF-FETCH via useReadingQuery. In sample mode the seeded sample QueryClient
// (CatalogDataSource.seedReadingSample) satisfies every ["reading-query", spec]
// key, so each tile renders POPULATED with ZERO network — the same no-network
// invariant the rest of this file guards. The windowed tiles derive their spec
// from the DEFAULT reading range (12w), so reset the module store first.
// ---------------------------------------------------------------------------
describe('CatalogWidgetRenderer(source="sample") — reading-domain kinds render from the seed', () => {
  beforeEach(() => __resetReadingRange());

  it("reading-listening-in-range shows the seeded scalar KPI", async () => {
    renderSample("reading-listening-in-range");
    await waitFor(() => expect(screen.getByText("42h 30m")).toBeInTheDocument());
    expect(screen.getByText("Listening in range")).toBeInTheDocument();
  });

  it("reading-books-by-genre renders the seeded genre legend (donut)", async () => {
    renderSample("reading-books-by-genre");
    await waitFor(() => expect(screen.getByText("Science Fiction")).toBeInTheDocument());
    expect(screen.getByText("Fantasy")).toBeInTheDocument();
  });

  it("reading-top-series renders the seeded series bars", async () => {
    renderSample("reading-top-series");
    await waitFor(() => expect(screen.getByText("The Expanse")).toBeInTheDocument());
    expect(screen.getAllByTestId("reading-bar").length).toBeGreaterThan(0);
  });

  it("reading-finished-per-month renders month buckets", async () => {
    renderSample("reading-finished-per-month");
    await waitFor(() => expect(screen.getByText("Feb 2026")).toBeInTheDocument());
  });

  it("reading-listening-trend renders both the listening + coding series legend", async () => {
    renderSample("reading-listening-trend");
    await waitFor(() => {
      const legend = screen.getByTestId("reading-trend-legend");
      expect(legend).toHaveTextContent("Listening");
      expect(legend).toHaveTextContent("Coding");
    });
  });

  it("no reading kind falls through to a 'not wired' placeholder", async () => {
    for (const kind of [
      "reading-listening-in-range",
      "reading-books-by-genre",
      "reading-top-series",
      "reading-finished-per-month",
      "reading-listening-trend",
    ]) {
      const { container, unmount } = renderSample(kind);
      await waitFor(() => expect(container.innerHTML.trim().length).toBeGreaterThan(0));
      expect(container.textContent, kind).not.toMatch(
        /No spec for|Unsupported primitive|No renderer for|No spec renderer for/,
      );
      unmount();
    }
  });
});

// ---------------------------------------------------------------------------
// source="mine" — a light smoke test (not the focus of this file) proving
// the "mine" composition (real OverviewDataProvider + the SAME
// useOverviewStats/Punchcard/Momentum/Sessions hooks the Overview page runs)
// wires together without crashing against a real (MSW-mocked) authed
// backend. Unlike sample mode this DOES hit the network, so every route the
// "both"/profile dispatch path needs is stubbed explicitly.
// ---------------------------------------------------------------------------
describe('CatalogWidgetRenderer(source="mine")', () => {
  function stubMineEndpoints() {
    server.use(
      http.get("/api/v1/users/current/profile", () =>
        HttpResponse.json({ enabled: false, slug: null }),
      ),
      http.get("/api/v1/users/current/stats/punchcard", () =>
        HttpResponse.json({ cells: [], maxSeconds: 0, totalSeconds: 0 }),
      ),
      http.get("/api/v1/users/current/stats/sessions", () =>
        HttpResponse.json({
          summary: { count: 0, totalSeconds: 0, avgSeconds: 0, maxSeconds: 0, medianSeconds: 0 },
          daily: [],
          histogram: [],
        }),
      ),
      http.get("/api/v1/users/current/stats/momentum", () =>
        HttpResponse.json({ weeks: [], projects: [] }),
      ),
    );
    // /api/v1/users/current/stats is already default-handled (factories'
    // statsPayload()); /auth/refresh_token is already default-handled too.
  }

  it("top-langs renders real (MSW-backed) data without crashing", async () => {
    stubMineEndpoints();
    const { container } = renderWithProviders(
      <CatalogWidgetRenderer kind="top-langs" source="mine" />,
      { withAuth: true, withRouter: true },
    );
    await waitFor(() => {
      expect(container.innerHTML.trim().length).toBeGreaterThan(0);
    });
    expect(container.textContent).not.toMatch(/No spec for|Unsupported primitive/);
  });

  it("overview-stats (self-fetching) renders real data without crashing", async () => {
    stubMineEndpoints();
    const { container } = renderWithProviders(
      <CatalogWidgetRenderer kind="overview-stats" source="mine" />,
      { withAuth: true, withRouter: true },
    );
    await waitFor(() => {
      expect(container.innerHTML.trim().length).toBeGreaterThan(0);
    });
  });
});
