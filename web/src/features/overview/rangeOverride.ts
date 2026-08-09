// rangeOverride (gaka-lzr Phase 5) — builds a derived TimeRangeControls for
// the CONFIGURE panel's per-tile "date range" override.
//
// Every Overview widget self-fetches by reading `tr` off the shared
// OverviewDataContext (see overviewWidgets.ts) — NOT via a param threaded
// through OverviewWidgetRenderer. So rather than plumbing a range override
// through every one of those hooks (a bespoke change per widget — exactly
// what the Phase 5 brief says NOT to build), OverviewWidgetRenderer nests a
// SECOND <OverviewDataProvider> around just the overridden tile's subtree,
// with `tr` swapped for the value this module builds. Every existing
// self-fetch hook picks the override up transparently via context — zero
// changes needed in overviewWidgets.ts, and it works uniformly for every
// kind (including the SpecRenderer-routed ones).
import type { TimeRangeControls } from "@/hooks/useTimeRange";

/** A "last N days ending now" TimeRangeControls, inheriting `timeLimit`
 * (the gap-fill cutoff) from `base` so the override only changes the WINDOW,
 * not the gap-detection behavior. The setters are no-ops: nothing inside an
 * overridden widget subtree renders the toolbar controls that would call
 * them (DateRangePicker/TimeLimitDropdown live in the page header, outside
 * any per-tile override scope). */
export function buildRangeOverrideTr(
  days: number,
  base: Pick<TimeRangeControls, "timeLimit">,
): TimeRangeControls {
  const end = new Date();
  const start = new Date(end);
  start.setDate(start.getDate() - days);
  return {
    start,
    end,
    numDays: days,
    timeLimit: base.timeLimit,
    startISO: start.toISOString(),
    endISO: end.toISOString(),
    setDaysFromToday: () => {},
    setRange: () => {},
    setTimeLimit: () => {},
  };
}
