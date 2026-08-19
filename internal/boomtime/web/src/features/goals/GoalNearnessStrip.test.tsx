// GoalNearnessStrip tests (gaka-cl9) — pin the at-a-glance overview's core
// behavior: one cell per goal (INCLUDING paused/disabled goals, shown dimmed as
// "—" when they have no evaluated progress), correct %, and the lastProgress
// fallback when the batch is missing a goal. Mocks the batched progress hook so
// the render logic is exercised in isolation.
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { GoalNearnessStrip } from "@boomtime/features/goals/GoalNearnessStrip";
import type { Goal } from "@/types/api";

vi.mock("@boomtime/features/goals/useGoals", () => ({
  useAllGoalProgress: () => ({
    data: {
      progress: {
        g1: { hit: true, progress: 1, subConditions: [] },
        g2: { hit: false, progress: 0.19, subConditions: [] },
      },
    },
  }),
}));

const mkGoal = (id: string, name: string, enabled = true): Goal =>
  ({
    id,
    name,
    description: null,
    spec: {},
    enabled,
    createdAt: "",
    updatedAt: "",
    lastEvaluatedAt: null,
    lastProgress: null,
  }) as unknown as Goal;

describe("GoalNearnessStrip", () => {
  it("renders a cell per goal with its progress %, INCLUDING paused ones", () => {
    render(
      <GoalNearnessStrip
        goals={[mkGoal("g1", "weekly-go"), mkGoal("g2", "deep-work"), mkGoal("g3", "paused-goal", false)]}
      />,
    );
    expect(screen.getByText("100%")).toBeInTheDocument();
    expect(screen.getByText("19%")).toBeInTheDocument();
    expect(screen.getByText("weekly-go")).toBeInTheDocument();
    // paused goal is now shown (dimmed), not filtered out; the batch has no
    // entry for it and it has no lastProgress, so it renders "—".
    expect(screen.getByText("paused-goal")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
    // header reflects the paused count
    expect(screen.getByText(/1 paused/)).toBeInTheDocument();
  });

  it("still renders a single paused goal (dimmed, no progress)", () => {
    render(<GoalNearnessStrip goals={[mkGoal("g3", "paused", false)]} />);
    expect(screen.getByText("paused")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("renders nothing when there are no goals at all", () => {
    const { container } = render(<GoalNearnessStrip goals={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("falls back to lastProgress when the batch lacks the goal", () => {
    const g = mkGoal("gX", "cold-goal");
    (g as unknown as { lastProgress: unknown }).lastProgress = { hit: false, progress: 0.4, subConditions: [] };
    render(<GoalNearnessStrip goals={[g]} />);
    expect(screen.getByText("40%")).toBeInTheDocument();
  });
});
