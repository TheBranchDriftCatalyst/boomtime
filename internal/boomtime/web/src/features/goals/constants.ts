// Shared constants for the goals feature (boom-wpb). Kept in a
// dedicated file so hooks, renderers, and the (rare) test file can
// all import without pulling the whole feature module in.

// Client-side mirror of stats.GoalCacheTTL (60s). React Query uses
// this as staleTime on goal-progress queries — the server also
// caches for the same window, so within-TTL reads may not even hit
// the network. A spec edit invalidates the RQ cache immediately via
// useGoalMutations; the server-side cache is cleared by the PATCH.
export const GoalCacheTTLMs = 60_000;

// Backend cap on predicate tree depth. Mirrors stats.MaxPredicateDepth.
// The recursive builder disables the "add group" affordance at this
// depth so users can't author a spec the server would reject.
export const MaxPredicateDepth = 5;

// Backend cap on streak min_days. Mirrors stats.MaxStreakDays.
export const MaxStreakDays = 365;
