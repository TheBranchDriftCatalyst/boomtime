// usePins — react-query binding for canonical-entity PINS (boom-canon).
//
// A pin is a curation rule with action="pin" that forces its (axis, value) to
// always get its own slice/bar and never fall into the bucket "Other" roll-up.
// The backend query engine auto-applies pins at group time, so the only FE work
// is: list the current pins (to render active state + find the rule id to
// remove), create a pin, remove a pin, and — crucially — invalidate the
// grouped-query caches so the chart refetches and the pinned value visibly
// escapes Other.
//
// The pins list lives under the ["curation", "pins"] key — a CHILD of the
// shared ["curation"] prefix — so any other curation write (via
// useCurationMutations, which invalidates qk.curation()) also refreshes this
// list, and our own writes here refresh the full curation list too.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { CurationRule } from "@shared/types/api";

// Child key of the ["curation"] prefix — see the module note above.
const PINS_KEY = ["curation", "pins"] as const;

// Grouped-query cache prefixes whose results the backend re-derives from pins.
// These are the inline keys the reading dashboard (useReadingQuery) and the
// Books explorer (BooksExplorer) build — invalidating the prefix refetches
// every open grouped chart so a freshly-pinned value escapes "Other".
const READING_QUERY_PREFIX = ["reading-query"] as const;
const BOOKS_EXPLORE_PREFIX = ["books-explore"] as const;

export function usePins() {
  const qc = useQueryClient();

  const pinsQuery = useQuery({
    queryKey: PINS_KEY,
    queryFn: () => api.listPins(),
  });

  function invalidatePinDependents() {
    // Refresh the pins list itself (and the full curation list via the prefix)…
    qc.invalidateQueries({ queryKey: qk.curation() });
    // …and every grouped chart that could now gain/lose an escaped slice.
    qc.invalidateQueries({ queryKey: READING_QUERY_PREFIX });
    qc.invalidateQueries({ queryKey: BOOKS_EXPLORE_PREFIX });
  }

  const pin = useMutation({
    mutationFn: (vars: { axis: string; value: string }) =>
      api.pinValue(vars.axis, vars.value),
    onSuccess: invalidatePinDependents,
  });

  const unpin = useMutation({
    mutationFn: (ruleId: number) => api.unpinValue(ruleId),
    onSuccess: invalidatePinDependents,
  });

  const pins: CurationRule[] = pinsQuery.data ?? [];

  // The pin rule for an (axis, value), or undefined when not pinned. Used to
  // both decide the toggle's active state and recover the rule id to unpin.
  function pinFor(axis: string, value: string): CurationRule | undefined {
    return pins.find((r) => r.axis === axis && r.matchValue === value);
  }

  return {
    pins,
    pinFor,
    pin,
    unpin,
    isLoading: pinsQuery.isLoading,
  };
}
