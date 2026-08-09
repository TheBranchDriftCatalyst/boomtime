// goalRenderers.test.tsx — tests for the three goal tile renderers
// (gaka-wpb): GoalProgress, GoalList, GoalRing. Each renderer is a
// thin display layer over useGoalsQuery + useAllGoalProgress; the
// invariants under test:
//
//   - EMPTY STATE: no enabled goals renders a specific placeholder
//     (users know to author one in Settings).
//   - DISABLED FILTER: goals with enabled=false MUST NOT appear on
//     any of the three renderers (disabled = "paused", not "hidden
//     from Settings").
//   - PROGRESS PRECEDENCE: batch-progress entry (fresh) takes
//     precedence over goal.lastProgress (cached at write time). If
//     the FE ever regressed to prefer lastProgress, a stale value
//     would be shown even after the batch call succeeded.
//   - PCT ROUNDING: displayed % = Math.round(progress * 100). A leaf
//     at 0.999 must show 100%, not 99% (matches server-side ceil
//     display convention).
//   - RING CAP: GoalRing slices to first 3 enabled goals — a
//     regression that showed all 6 goals would flood the layout.
//
// gaka-wpb.1 (audit): the goals agent's initial commit shipped these
// renderers without tests. This file plugs the gap so a future
// refactor that (say) swaps `batch?.progress?.[g.id] ?? lastProgress`
// for the reverse fallback order gets caught here.
import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { GoalList } from "@/features/widgets/renderers/GoalList";
import { GoalProgress } from "@/features/widgets/renderers/GoalProgress";
import { GoalRing } from "@/features/widgets/renderers/GoalRing";
import type { Goal, GoalProgress as GoalProgressT } from "@/types/api";

// Small factory so tests can build tiny variants of Goals without
// repeating the boilerplate fields.
function makeGoal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: overrides.id ?? "g-" + Math.random().toString(36).slice(2, 8),
    owner: "alice",
    name: "weekly-go",
    description: null,
    spec: {
      kind: "time",
      axis: "language",
      value: "Go",
      op: ">=",
      target_seconds: 3600,
      window: "week",
    },
    enabled: true,
    public: false,
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-01T00:00:00Z",
    lastEvaluatedAt: null,
    lastProgress: null,
    ...overrides,
  };
}

function makeProgress(overrides: Partial<GoalProgressT> = {}): GoalProgressT {
  return {
    hit: false,
    progress: 0,
    sub_conditions: [],
    ...overrides,
  };
}

// Convenience: wire the two endpoints all three renderers hit.
function stubGoalsAndProgress(
  goals: Goal[],
  progress: Record<string, GoalProgressT>,
) {
  server.use(
    http.get("/api/v1/users/current/goals", () =>
      HttpResponse.json({ goals }),
    ),
    http.get("/api/v1/users/current/goals/progress", () =>
      HttpResponse.json({ progress }),
    ),
  );
}

describe("GoalProgress renderer (gaka-wpb)", () => {
  it("renders 'No goals yet' placeholder when no enabled goals exist", async () => {
    stubGoalsAndProgress([], {});
    renderWithProviders(<GoalProgress />);
    await waitFor(() =>
      expect(screen.getByText(/no goals yet/i)).toBeInTheDocument(),
    );
  });

  it("hides disabled goals — placeholder shows even with a disabled goal in the list", async () => {
    // Load-bearing: enabled=false is "paused", not "invisible only in
    // Settings". If the filter drifted (e.g. showing disabled with
    // opacity), the placeholder wouldn't render.
    stubGoalsAndProgress(
      [makeGoal({ id: "g-off", name: "paused", enabled: false })],
      {},
    );
    renderWithProviders(<GoalProgress />);
    await waitFor(() =>
      expect(screen.getByText(/no goals yet/i)).toBeInTheDocument(),
    );
    // The paused goal's name must NOT appear.
    expect(screen.queryByText("paused")).not.toBeInTheDocument();
  });

  it("renders the first enabled goal with % from the batch (not lastProgress)", async () => {
    // Stale lastProgress + fresh batch progress. The batch value MUST
    // win — otherwise a spec edit's freshly-recomputed value would be
    // masked by the persisted-at-write-time last_progress.
    const goal = makeGoal({
      id: "g-fresh",
      name: "weekly-python",
      lastProgress: makeProgress({ progress: 0.1, hit: false }),
    });
    stubGoalsAndProgress([goal], {
      "g-fresh": makeProgress({ progress: 0.75, hit: false }),
    });
    renderWithProviders(<GoalProgress />);
    await waitFor(() =>
      expect(screen.getByText("weekly-python")).toBeInTheDocument(),
    );
    // 0.75 → 75%. A regression that used lastProgress instead would
    // show 10%.
    expect(screen.getByText("75%")).toBeInTheDocument();
    expect(screen.queryByText("10%")).not.toBeInTheDocument();
  });

  it("rounds progress to nearest whole percent (0.999 → 100)", async () => {
    // Load-bearing: Math.round vs Math.floor drift. Server sums are
    // typically clamped 0..1 with float noise; near-100% must display
    // as 100%, not 99%.
    const goal = makeGoal({ id: "g-round" });
    stubGoalsAndProgress([goal], {
      "g-round": makeProgress({ progress: 0.999, hit: true }),
    });
    renderWithProviders(<GoalProgress />);
    await waitFor(() =>
      expect(screen.getByText("100%")).toBeInTheDocument(),
    );
  });
});

describe("GoalList renderer (gaka-wpb)", () => {
  it("renders 'No goals yet' when the enabled list is empty", async () => {
    stubGoalsAndProgress(
      [makeGoal({ enabled: false, name: "hidden" })],
      {},
    );
    renderWithProviders(<GoalList />);
    await waitFor(() =>
      expect(screen.getByText(/no goals yet/i)).toBeInTheDocument(),
    );
    expect(screen.queryByText("hidden")).not.toBeInTheDocument();
  });

  it("lists every enabled goal (skips disabled) with per-row pct from the batch", async () => {
    // Three goals: one disabled (must not render), two enabled with
    // distinct pcts (must render distinguishably).
    const goals = [
      makeGoal({ id: "g-a", name: "goal-a" }),
      makeGoal({ id: "g-off", name: "goal-off", enabled: false }),
      makeGoal({ id: "g-b", name: "goal-b" }),
    ];
    stubGoalsAndProgress(goals, {
      "g-a": makeProgress({ progress: 0.5, hit: false }),
      "g-b": makeProgress({ progress: 1, hit: true }),
    });
    renderWithProviders(<GoalList />);
    await waitFor(() =>
      expect(screen.getByText("goal-a")).toBeInTheDocument(),
    );
    expect(screen.getByText("goal-b")).toBeInTheDocument();
    expect(screen.queryByText("goal-off")).not.toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("100%")).toBeInTheDocument();
  });

  it("falls back to lastProgress when batch entry missing (avoids flash-of-empty)", async () => {
    // Batch didn't include this goal's id (e.g. disabled at time of
    // batch call but flipped since; or slow re-fetch). lastProgress
    // is the persistent snapshot — the UX contract is to render it
    // rather than 0%.
    const goal = makeGoal({
      id: "g-fallback",
      name: "with-fallback",
      lastProgress: makeProgress({ progress: 0.42, hit: false }),
    });
    stubGoalsAndProgress([goal], {}); // batch empty
    renderWithProviders(<GoalList />);
    await waitFor(() =>
      expect(screen.getByText("with-fallback")).toBeInTheDocument(),
    );
    // 0.42 → 42%.
    expect(screen.getByText("42%")).toBeInTheDocument();
  });
});

describe("GoalRing renderer (gaka-wpb)", () => {
  it("renders 'No goals yet' when no enabled goals exist", async () => {
    stubGoalsAndProgress([], {});
    renderWithProviders(<GoalRing />);
    await waitFor(() =>
      expect(screen.getByText(/no goals yet/i)).toBeInTheDocument(),
    );
  });

  it("shows AT MOST 3 goals in the legend (slice cap)", async () => {
    // Five enabled goals — legend must list exactly 3 (a regression
    // that lifted the cap to 5 would show all).
    const goals = [1, 2, 3, 4, 5].map((n) =>
      makeGoal({ id: "g-" + n, name: "goal-" + n }),
    );
    const progress: Record<string, GoalProgressT> = {};
    goals.forEach((g) => {
      progress[g.id] = makeProgress({ progress: 0.5 });
    });
    stubGoalsAndProgress(goals, progress);
    renderWithProviders(<GoalRing />);
    await waitFor(() =>
      expect(screen.getByText("goal-1")).toBeInTheDocument(),
    );
    // First 3 render; goal-4 and goal-5 must not.
    expect(screen.getByText("goal-2")).toBeInTheDocument();
    expect(screen.getByText("goal-3")).toBeInTheDocument();
    expect(screen.queryByText("goal-4")).not.toBeInTheDocument();
    expect(screen.queryByText("goal-5")).not.toBeInTheDocument();
  });

  it("legend pct prefers batch over lastProgress (freshness)", async () => {
    // One enabled goal with a stale lastProgress at 10% and a fresh
    // batch entry at 60%. The legend text must show 60%, not 10%.
    const goal = makeGoal({
      id: "g-legend",
      name: "legend-goal",
      lastProgress: makeProgress({ progress: 0.1 }),
    });
    stubGoalsAndProgress([goal], {
      "g-legend": makeProgress({ progress: 0.6 }),
    });
    renderWithProviders(<GoalRing />);
    await waitFor(() =>
      expect(screen.getByText("legend-goal")).toBeInTheDocument(),
    );
    expect(screen.getByText("60%")).toBeInTheDocument();
    expect(screen.queryByText("10%")).not.toBeInTheDocument();
  });
});
