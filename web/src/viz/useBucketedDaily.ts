import { useCallback, useMemo } from "react";
import { daysBetween } from "@/lib/utils";
import { bucketAvg, bucketDates, bucketGroups, bucketSum } from "@/viz/bucket";

/**
 * Shared ~weekly bucketing over a daily time-series range (see @/viz/bucket):
 * turns a [startDate, endDate] range into stable `chartDates` plus `sum`/`avg`
 * bucketing callbacks, so long ranges stay bounded (~60 x-points) instead of
 * rendering hundreds of daily columns on "All time".
 *
 * `sum` and `avg` are referentially stable while the bucket layout is
 * unchanged, so consumers can list them in useMemo deps without eslint
 * escape hatches.
 */
export function useBucketedDaily(startDate?: string, endDate?: string) {
  const dates = useMemo(
    () =>
      startDate && endDate
        ? daysBetween(new Date(startDate), new Date(endDate))
        : [],
    [startDate, endDate],
  );

  const groups = useMemo(() => bucketGroups(dates.length), [dates.length]);
  const chartDates = useMemo(() => bucketDates(groups, dates), [groups, dates]);

  /** Sum a daily numeric series into bucket totals. */
  const sum = useCallback((arr: number[]) => bucketSum(groups, arr), [groups]);
  /** Average a daily series over each bucket (ratios/rates/distinct counts). */
  const avg = useCallback((arr: number[]) => bucketAvg(groups, arr), [groups]);

  return { dates, groups, chartDates, sum, avg };
}
