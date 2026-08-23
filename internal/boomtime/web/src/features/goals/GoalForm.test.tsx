// GoalForm.test.tsx — the "Public" toggle (Part B Stage 4, boom-wpb). Two
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
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { GoalForm } from "@boomtime/features/goals/GoalForm";
import type { Goal } from "@shared/types/api";

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

  it("create: 'Listening time (Audible)' metric submits a reading-source weekly spec with no axis", async () => {
    let captured: Record<string, unknown> | undefined;
    server.use(
      http.post("/api/v1/users/current/goals", async ({ request }) => {
        captured = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ goal: makeGoal({ id: "new-3" }) });
      }),
    );

    renderWithProviders(<GoalForm open onOpenChange={() => {}} editing={null} />);

    await userEvent.type(screen.getByLabelText("Name"), "Listen 5h/week");
    await userEvent.click(screen.getByTestId("metric-listening"));
    // The reading leaf renders a fixed metric label instead of an axis picker.
    expect(screen.getByTestId("reading-metric-label")).toHaveTextContent(
      "Listening time (Audible)",
    );
    await userEvent.click(screen.getByRole("button", { name: "Create goal" }));

    await waitFor(() => expect(captured).toBeDefined());
    const spec = captured!.spec as Record<string, unknown>;
    expect(spec.kind).toBe("time");
    expect(spec.source).toBe("reading");
    expect(spec.window).toBe("week");
    expect(spec.target_seconds).toBe(5 * 3600);
    // v1 reading goals carry NO axis (backend rejects one).
    expect(spec.axis).toBeUndefined();
  });

  it("create: the metric picker highlights the active source (boom-bs5l)", async () => {
    renderWithProviders(<GoalForm open onOpenChange={() => {}} editing={null} />);
    const coding = screen.getByTestId("metric-coding");
    const listening = screen.getByTestId("metric-listening");

    // Default spec is a coding time leaf → Coding is the active metric.
    expect(coding).toHaveAttribute("aria-pressed", "true");
    expect(listening).toHaveAttribute("aria-pressed", "false");

    // Switch to the listening template → highlight follows the spec source.
    await userEvent.click(listening);
    expect(listening).toHaveAttribute("aria-pressed", "true");
    expect(coding).toHaveAttribute("aria-pressed", "false");
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
