// GoalForm.test.tsx — the "Public" toggle (Part B Stage 4, gaka-wpb). Two
// invariants:
//
//   - Create: the toggle defaults OFF (private) and the submitted body
//     carries `public` matching whatever the user set it to.
//   - Edit: the toggle is PRE-FILLED from `editing.public` (proves the
//     round trip both ways — not just "the toggle exists").
//
// The recursive PredicateBuilder itself is covered by
// PredicateBuilder.test.tsx; this file only cares about the Public field.
import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import { GoalForm } from "@/features/goals/GoalForm";
import type { Goal } from "@/types/api";

function makeGoal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: "g-edit",
    owner: "alice",
    name: "weekly-go",
    description: null,
    spec: { kind: "time", axis: "language", value: "Go", op: ">=", target_seconds: 3600, window: "week" },
    enabled: true,
    public: false,
    createdAt: "2026-07-01T00:00:00Z",
    updatedAt: "2026-07-01T00:00:00Z",
    lastEvaluatedAt: null,
    lastProgress: null,
    ...overrides,
  };
}

describe("GoalForm Public toggle (Part B Stage 4)", () => {
  it("create: defaults to private (public:false) when the toggle is left untouched", async () => {
    let captured: Record<string, unknown> | undefined;
    server.use(
      http.post("/api/v1/users/current/goals", async ({ request }) => {
        captured = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ goal: makeGoal({ id: "new-1" }) });
      }),
    );

    renderWithProviders(<GoalForm open onOpenChange={() => {}} editing={null} />);

    await userEvent.type(screen.getByLabelText("Name"), "New Goal");
    await userEvent.click(screen.getByRole("button", { name: "Create goal" }));

    await waitFor(() => expect(captured).toBeDefined());
    expect(captured!.public).toBe(false);
  });

  it("create: flipping the Public switch on sends public:true", async () => {
    let captured: Record<string, unknown> | undefined;
    server.use(
      http.post("/api/v1/users/current/goals", async ({ request }) => {
        captured = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ goal: makeGoal({ id: "new-2", public: true }) });
      }),
    );

    renderWithProviders(<GoalForm open onOpenChange={() => {}} editing={null} />);

    await userEvent.type(screen.getByLabelText("Name"), "Public Goal");
    await userEvent.click(screen.getByTestId("goal-public-switch"));
    await userEvent.click(screen.getByRole("button", { name: "Create goal" }));

    await waitFor(() => expect(captured).toBeDefined());
    expect(captured!.public).toBe(true);
  });

  it("edit: the switch is pre-filled from editing.public — flipping it off sends public:false on save", async () => {
    let captured: Record<string, unknown> | undefined;
    server.use(
      http.patch("/api/v1/users/current/goals/g-edit", async ({ request }) => {
        captured = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ goal: makeGoal({ public: false }) });
      }),
    );

    const editing = makeGoal({ public: true });
    renderWithProviders(<GoalForm open onOpenChange={() => {}} editing={editing} />);

    // Pre-filled ON, matching editing.public=true.
    expect(screen.getByTestId("goal-public-switch")).toHaveAttribute("aria-checked", "true");

    await userEvent.click(screen.getByTestId("goal-public-switch"));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(captured).toBeDefined());
    expect(captured!.public).toBe(false);
  });
});
