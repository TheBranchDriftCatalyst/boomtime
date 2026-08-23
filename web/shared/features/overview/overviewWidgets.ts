// overviewWidgets (boom-38v) — per-widget self-fetch hooks for the Overview
// dashboard. Each reads the shared OverviewDataContext and runs EXACTLY the
// query (same qk.* key + params) the legacy inline OverviewDashboard runs, so
// when Phase 3 swaps the static ChartCard grid for a draggable widget grid,
// every widget fetches through the same react-query cache — no extra network.
//
// The stats hook also carries the day-bucketing + category-series derivations
// verbatim from OverviewDashboard, so the widgets that need them (column,
// heatmaps, cumulative, streamgraph, stat tiles) get identical inputs.
import { useCallback, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { removeHours } from "@shared/lib/utils";
import { mostActive } from "@shared/lib/mostActive";
import { orderCategories, paletteByName } from "@shared/viz/d3/color";
import { useBucketedDaily } from "@shared/viz/useBucketedDaily";
import type { ResourceStats } from "@shared/types/api";
import { useOverviewData } from "./OverviewDataContext";

/** Stats query + all the day-bucketed / category-series derivations the
 * Overview time charts + stat tiles consume. Mirrors OverviewDashboard's
 * inline logic 1:1. */
export function useOverviewStats() {
  const { tr, space } = useOverviewData();

  const query = useQuery({
    queryKey: qk.stats(tr.startISO, tr.endISO, tr.timeLimit, space),
    queryFn: () =>
      api.getStats({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
  const stats = query.data;

  const { dates, chartDates, sum } = useBucketedDaily(
    stats?.startDate,
    stats?.endDate,
  );
  const chartDailyTotal = useMemo(
    () => sum(stats?.dailyTotal ?? []),
    [sum, stats?.dailyTotal],
  );
  const bucketItems = useCallback(
    (items: ResourceStats[]) =>
      items.map((it) => ({ ...it, totalDaily: sum(it.totalDaily) })),
    [sum],
  );
  const chartProjects = useMemo(
    () => bucketItems(stats?.projects ?? []),
    [bucketItems, stats?.projects],
  );
  const chartLanguages = useMemo(
    () => bucketItems(stats?.languages ?? []),
    [bucketItems, stats?.languages],
  );
  const chartCategories = useMemo(
    () => bucketItems(stats?.categories ?? []),
    [bucketItems, stats?.categories],
  );
  const categoryColumnSeries = useMemo(() => {
    const ordered = orderCategories(chartCategories);
    const palette = paletteByName(ordered);
    return ordered.map((c) => ({
      name: c.name,
      values: c.totalDaily,
      color: palette.get(c.name)!,
      otherMembers: c.otherMembers,
      otherCount: c.otherCount,
    }));
  }, [chartCategories]);
  const mostActiveProject = mostActive(stats?.projects ?? []);
  const mostActiveLang = mostActive(stats?.languages ?? []);

  return {
    query,
    stats,
    dates,
    chartDates,
    chartDailyTotal,
    chartProjects,
    chartLanguages,
    chartCategories,
    categoryColumnSeries,
    mostActiveProject,
    mostActiveLang,
  };
}

export function useOverviewTimeline() {
  const { tr, timelineHours, space } = useOverviewData();
  return useQuery({
    queryKey: qk.timeline(timelineHours, tr.timeLimit, space),
    queryFn: () =>
      api.getTimeline({
        start: removeHours(new Date(), timelineHours).toISOString(),
        end: new Date().toISOString(),
        timeLimit: tr.timeLimit,
        space,
      }),
  });
}

export function useOverviewPunchcard() {
  const { tr, space } = useOverviewData();
  return useQuery({
    queryKey: qk.punchcard(tr.startISO, tr.endISO, tr.timeLimit, space),
    queryFn: () =>
      api.getPunchcard({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
}

export function useOverviewSessions() {
  const { tr, space } = useOverviewData();
  return useQuery({
    queryKey: qk.sessions(tr.startISO, tr.endISO, tr.timeLimit, space),
    queryFn: () =>
      api.getSessions({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
}

export function useOverviewMomentum() {
  const { tr, space } = useOverviewData();
  return useQuery({
    queryKey: qk.momentum(tr.startISO, tr.endISO, space),
    queryFn: () =>
      api.getMomentum({ start: tr.startISO, end: tr.endISO, top: 8, space }),
  });
}

// boom-yfg: lines-of-code (total + per-project + over-time). Same range/space
// scoping as the other Overview widgets so a scoped/renamed dashboard's LOC
// tile refetches with the rest.
export function useOverviewLoc() {
  const { tr, space } = useOverviewData();
  return useQuery({
    queryKey: qk.loc(tr.startISO, tr.endISO, space),
    queryFn: () =>
      api.getLoc({ start: tr.startISO, end: tr.endISO, space }),
  });
}

export function useOverviewAIActivity() {
  const { tr } = useOverviewData();
  return useQuery({
    queryKey: qk.aiActivity(tr.startISO, tr.endISO),
    queryFn: () => api.getAIActivity({ start: tr.startISO, end: tr.endISO }),
  });
}

export function useOverviewHealthActivity() {
  const { tr } = useOverviewData();
  return useQuery({
    queryKey: qk.healthActivity(tr.startISO, tr.endISO),
    queryFn: () =>
      api.getHealthActivity({ start: tr.startISO, end: tr.endISO }),
  });
}
