// CatalogWidgetRenderer — renders ANY of the 40 widget catalog kinds INLINE
// (real React DOM / inline SVG — never the <object type=image/svg+xml> SVG
// embed), from a SWITCHABLE data source ("mine" real data | "sample" fixture
// data). Built entirely on TOP of the two existing in-page dispatchers
// rather than reinventing them:
//
//   - WidgetRenderer.tsx: handles EVERY target:"both" kind unconditionally
//     (routes to SpecRenderer, keyed off a PublicDashboardPayload-shaped
//     `data` prop) PLUS the profile-scoped fe-only kinds (hero-identity,
//     grade-badge, labels-showcase, github-stats).
//   - OverviewWidgetRenderer.tsx: the Overview-scoped fe-only kinds that
//     self-fetch from OverviewDataContext (overview-stats, ai-assistance,
//     wellness, category-breakdown, streak-banner, overview-total-activity,
//     category-streamgraph, overview-timeline, loc, github-commits/
//     repos/languages).
//
// So this file's whole job is DATA PLUMBING: for "sample", wrap the render
// tree in the pre-seeded QueryClient + a fixed OverviewDataProvider (see
// CatalogDataSource.tsx) and hand WidgetRenderer a static fixture payload;
// for "mine", wrap in a REAL OverviewDataProvider (built from `rangeDays`)
// and hand WidgetRenderer a payload composed from the SAME self-fetch hooks
// (useOverviewStats/useOverviewPunchcard/useOverviewMomentum/
// useOverviewSessions) the Overview page itself runs.
//
// rangeDays: honored for source="mine" (it drives the real query window via
// buildTimeRangeControls). source="sample" intentionally ALWAYS renders its
// fixed, internally-consistent 90-day fixture regardless of rangeDays —
// re-deriving momentum/sessions/heatmaps for an arbitrary window from static
// fixtures is a re-generation problem disproportionate to a foundation
// layer; the fixture is generated once at a realistic 90-day width (see
// sampleData.ts's SAMPLE_RANGE_DAYS) and stays that width.
import { useMemo, useState, type ComponentType } from "react";
import { QueryClientProvider, useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { useAuth } from "@shared/features/auth/useAuth";
import { OverviewDataProvider } from "@shared/features/overview/OverviewDataContext";
import {
  useOverviewMomentum,
  useOverviewPunchcard,
  useOverviewSessions,
  useOverviewStats,
} from "@shared/features/overview/overviewWidgets";
import { WidgetRenderer } from "@shared/features/widgets/renderers/WidgetRenderer";
import { OverviewWidgetRenderer } from "@shared/features/widgets/renderers/OverviewWidgetRenderer";
import {
  BooksByGenreTile,
  FinishedPerMonthTile,
  ListeningThisWeekTile,
  ListeningTrendTile,
  TopSeriesByRuntimeTile,
} from "@shared/features/overview/reading/ReadingTiles";
import {
  SAMPLE_CATALOG_PAYLOAD,
  buildRealCatalogPayload,
  buildRealOverviewValue,
  buildSampleOverviewValue,
  getSampleQueryClient,
  type CatalogPayload,
  type CatalogSource,
} from "./CatalogDataSource";
import { SAMPLE_TIMELINE_HOURS, SAMPLE_USERNAME } from "./sampleData";

const DEFAULT_RANGE_DAYS = 90;

/** fe-only kinds that self-fetch via OverviewWidgetRenderer's
 * OverviewDataContext hooks. Every OTHER kind (every target:"both" kind,
 * PLUS the profile-scoped fe-only kinds hero-identity/grade-badge/
 * labels-showcase/github-stats) routes through WidgetRenderer instead — see
 * the file doc. Kept as a Set (not a switch) so it's trivially diffable
 * against catalog.ts's dashboardScopes if a future kind moves scopes. */
const OVERVIEW_ONLY_FE_KINDS = new Set<string>([
  "loc",
  "overview-stats",
  "ai-assistance",
  "wellness",
  "category-breakdown",
  "streak-banner",
  "overview-total-activity",
  "category-streamgraph",
  "overview-timeline",
  "github-commits",
  "github-repos",
  "github-languages",
]);

// boom-qcxg: reading-domain fe-only kinds → their existing Reading dashboard
// tile. Each tile SELF-FETCHES via useReadingQuery (runQuery, POST
// /api/v1/query) under whichever QueryClient wraps it — the seeded sample
// client in sample mode, the app's ambient client in mine mode — so one
// component serves BOTH sources (no separate sample/mine branch needed, unlike
// the overview kinds that read an injected OverviewDataContext). Kept as a map
// so a card renders the tile directly; the tile owns its own ChartCard chrome.
const READING_KINDS: Record<string, ComponentType> = {
  "reading-listening-trend": ListeningTrendTile,
  "reading-books-by-genre": BooksByGenreTile,
  "reading-top-series": TopSeriesByRuntimeTile,
  "reading-finished-per-month": FinishedPerMonthTile,
  "reading-listening-in-range": ListeningThisWeekTile,
};

export interface CatalogWidgetRendererProps {
  kind: string;
  source: CatalogSource;
  rangeDays?: number;
}

/** Renders any of the 40 catalog kinds inline from the given source. Graceful
 * empty/loading states come for free from the underlying WidgetRenderer /
 * OverviewWidgetRenderer / SpecRenderer components — this file adds no
 * additional loading UI of its own. */
export function CatalogWidgetRenderer({
  kind,
  source,
  rangeDays = DEFAULT_RANGE_DAYS,
}: CatalogWidgetRendererProps) {
  if (source === "sample") {
    return <SampleCatalogWidget kind={kind} />;
  }
  return <MineCatalogWidget kind={kind} rangeDays={rangeDays} />;
}

// ---------------------------------------------------------------------------
// Shared leaf dispatch — takes an ALREADY-RESOLVED payload + slug and picks
// WidgetRenderer vs OverviewWidgetRenderer. Calls no hooks of its own, so
// both source branches can share it without violating the rules of hooks.
// ---------------------------------------------------------------------------
function CatalogKindDispatch({
  kind,
  payload,
  slug,
}: {
  kind: string;
  payload: CatalogPayload;
  slug: string | undefined;
}) {
  const Reading = READING_KINDS[kind];
  if (Reading) return <Reading />;
  if (OVERVIEW_ONLY_FE_KINDS.has(kind)) {
    return <OverviewWidgetRenderer kind={kind} />;
  }
  return <WidgetRenderer kind={kind} data={payload} slug={slug} />;
}

// ---------------------------------------------------------------------------
// Sample mode
// ---------------------------------------------------------------------------
function SampleCatalogWidget({ kind }: { kind: string }) {
  // Module-level singleton — shared across every sample-mode card on the
  // page, seeded once (see getSampleQueryClient's doc comment).
  const qc = useMemo(() => getSampleQueryClient(), []);
  const [timelineHours, setTimelineHours] = useState(SAMPLE_TIMELINE_HOURS);
  const overviewValue = useMemo(
    () => buildSampleOverviewValue(timelineHours, setTimelineHours),
    [timelineHours],
  );
  return (
    <QueryClientProvider client={qc}>
      <OverviewDataProvider value={overviewValue}>
        <CatalogKindDispatch kind={kind} payload={SAMPLE_CATALOG_PAYLOAD} slug={SAMPLE_USERNAME} />
      </OverviewDataProvider>
    </QueryClientProvider>
  );
}

// ---------------------------------------------------------------------------
// Mine mode — uses the app's AMBIENT QueryClientProvider (no nesting), a
// REAL OverviewDataProvider built from rangeDays, and the caller's own
// authed data.
// ---------------------------------------------------------------------------
function MineCatalogWidget({ kind, rangeDays }: { kind: string; rangeDays: number }) {
  const [timelineHours, setTimelineHours] = useState(SAMPLE_TIMELINE_HOURS);
  const overviewValue = useMemo(
    () => buildRealOverviewValue(rangeDays, timelineHours, setTimelineHours),
    [rangeDays, timelineHours],
  );
  return (
    <OverviewDataProvider value={overviewValue}>
      <MineCatalogKindDispatch kind={kind} />
    </OverviewDataProvider>
  );
}

// Top-level switch, zero hooks of its own (HOOKS RULE — mirrors
// OverviewWidgetRenderer.tsx's own file-doc convention): each branch is a
// leaf component that owns exactly the hooks its path needs.
function MineCatalogKindDispatch({ kind }: { kind: string }) {
  const Reading = READING_KINDS[kind];
  if (Reading) return <Reading />;
  if (OVERVIEW_ONLY_FE_KINDS.has(kind)) {
    return <OverviewWidgetRenderer kind={kind} />;
  }
  return <MineBothOrProfileKind kind={kind} />;
}

/** Every target:"both" kind PLUS the profile-scoped fe-only kinds
 * (hero-identity/grade-badge/labels-showcase/github-stats), fed REAL data
 * via the SAME hooks the Overview page runs. `slug` mirrors
 * InAppProfilePage.tsx's own resolution (`mine.slug ?? username`) so
 * github-stats's public-mirror fetch and any future slug-scoped kind behave
 * identically to the owner's real profile preview. */
function MineBothOrProfileKind({ kind }: { kind: string }) {
  const { username } = useAuth();
  const { data: profile } = useQuery({
    queryKey: qk.publicProfile(),
    queryFn: () => api.getPublicProfile(),
    staleTime: 30_000,
    retry: false,
  });
  const { stats } = useOverviewStats();
  const punchcardQuery = useOverviewPunchcard();
  const momentumQuery = useOverviewMomentum();
  const sessionsQuery = useOverviewSessions();

  const slug = (profile?.slug ?? username ?? "").trim() || undefined;
  const payload = buildRealCatalogPayload(
    username,
    stats,
    punchcardQuery.data,
    momentumQuery.data,
    sessionsQuery.data,
  );
  return <WidgetRenderer kind={kind} data={payload} slug={slug} />;
}
