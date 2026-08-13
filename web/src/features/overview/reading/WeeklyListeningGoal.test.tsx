// WeeklyListeningGoal.test.tsx — the goal tile renders a progress ring when the
// user has an enabled weekly reading-time goal, and a "set a listening goal"
// CTA (linking to /app/goals) otherwise. Read-only against the goals API,
// stubbed via MSW.
import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { WeeklyListeningGoalTile } from "./WeeklyListeningGoal";
import type { Goal } from "@/types/api";

function readingWeekGoal(): Goal {
  return {
    id: "g1",
    owner: "me",
    name: "Listen 5h / week",
    description: null,
    spec: {
      kind: "time",
      source: "reading",
      op: ">=",
      target_seconds: 5 * 3600,
      window: "week",
    },
    enabled: true,
    public: false,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    lastEvaluatedAt: null,
    lastProgress: null,
  };
}

describe("WeeklyListeningGoalTile", () => {
  it("renders the ring for an enabled weekly reading goal", async () => {
    server.use(
      http.get("/api/v1/users/current/goals", () =>
        HttpResponse.json({ goals: [readingWeekGoal()] }),
      ),
      http.get("/api/v1/users/current/goals/g1/progress", () =>
        HttpResponse.json({ hit: false, progress: 0.5, sub_conditions: [] }),
      ),
    );
    renderWithProviders(<WeeklyListeningGoalTile />, { withRouter: true });

    expect(await screen.findByTestId("listening-goal-ring")).toBeInTheDocument();
    // The ring paints 0% from lastProgress first, then the progress query
    // resolves to 50% — wait for the mapped value.
    expect(await screen.findByText("50%")).toBeInTheDocument();
    expect(screen.getByText("Listen 5h / week")).toBeInTheDocument();
  });

  it("renders the CTA linking to /app/goals when there is no listening goal", async () => {
    server.use(
      http.get("/api/v1/users/current/goals", () =>
        HttpResponse.json({ goals: [] }),
      ),
    );
    renderWithProviders(<WeeklyListeningGoalTile />, { withRouter: true });

    const cta = await screen.findByTestId("listening-goal-cta");
    expect(cta).toBeInTheDocument();
    const link = screen.getByRole("link", { name: /set a listening goal/i });
    expect(link).toHaveAttribute("href", "/app/goals");
  });

  it("ignores a coding-only goal (renders the CTA)", async () => {
    const coding: Goal = {
      ...readingWeekGoal(),
      id: "c1",
      name: "Code 20h / week",
      spec: {
        kind: "time",
        source: "coding",
        op: ">=",
        target_seconds: 20 * 3600,
        window: "week",
      },
    };
    server.use(
      http.get("/api/v1/users/current/goals", () =>
        HttpResponse.json({ goals: [coding] }),
      ),
    );
    renderWithProviders(<WeeklyListeningGoalTile />, { withRouter: true });
    expect(await screen.findByTestId("listening-goal-cta")).toBeInTheDocument();
  });
});
