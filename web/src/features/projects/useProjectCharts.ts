import { useMemo } from "react";
import { MIN_SLICE_SECONDS, colorAt, paletteByName } from "@/viz/d3/color";
import { useBucketedDaily } from "@/viz/useBucketedDaily";
import type { ProjectStatistics } from "@/types/api";

/**
 * Bucketed chart series for a project's detail charts. Buckets the daily
 * activity into ~weekly groups on long ranges so the column chart stays
 * bounded (~60 points) instead of freezing on "All time".
 */
export function useProjectCharts(stats: ProjectStatistics | undefined) {
  const { chartDates, sum, avg } = useBucketedDaily(
    stats?.startDate,
    stats?.endDate,
  );

  const chartDailyTotal = useMemo(
    () => sum(stats?.dailyTotal ?? []),
    [sum, stats?.dailyTotal],
  );

  // Stacked-column series for the project "Total activity", stacked by
  // language. Buckets each language's per-day series with the SAME bucket
  // layout as chartDailyTotal so the x-axis aligns and All-time stays bounded,
  // and per-day column sums still equal chartDailyTotal. Colors come from the
  // SAME `paletteByName` contract the Language pie uses (>=MIN_SLICE_SECONDS
  // filter, positional palette over stats.languages), so the pie and stacked
  // bars cannot desync.
  const languageColumnSeries = useMemo(() => {
    const langsDaily = stats?.languagesDaily;
    if (!langsDaily || langsDaily.length === 0) return [];
    const colorByName = paletteByName(stats?.languages ?? [], {
      minSeconds: MIN_SLICE_SECONDS,
    });
    // gaka-7m4: languagesDaily doesn't carry otherMembers (Name+Daily only),
    // but stats.languages (the capped ResourceStats list) does. Look each
    // name up so the "Other (N more)" stacked segment can render a breakdown.
    const langByName = new Map(
      (stats?.languages ?? []).map((l) => [l.name, l]),
    );
    return langsDaily
      .map((ld, i) => {
        const source = langByName.get(ld.name);
        return {
          name: ld.name,
          values: sum(ld.daily),
          // Fall back to the by-index color (matches the pie's positional
          // palette) for names the pie filtered out (<60s), so every segment
          // stays colored.
          color: colorByName.get(ld.name) ?? colorAt(i),
          otherMembers: source?.otherMembers,
          otherCount: source?.otherCount,
        };
      })
      .filter((s) => s.values.some((v) => v > 0));
  }, [sum, stats?.languagesDaily, stats?.languages]);

  // Bucketed series for the viz Projects charts. Ratio + entities are averaged
  // over each bucket (summing daily distinct counts would double-count files
  // touched on multiple days).
  const chartWriteRatio = useMemo(
    () => avg(stats?.dailyWriteRatio ?? []),
    [avg, stats?.dailyWriteRatio],
  );
  const chartEntities = useMemo(
    () => avg(stats?.dailyEntities ?? []).map(Math.round),
    [avg, stats?.dailyEntities],
  );

  return {
    chartDates,
    chartDailyTotal,
    languageColumnSeries,
    chartWriteRatio,
    chartEntities,
  };
}
