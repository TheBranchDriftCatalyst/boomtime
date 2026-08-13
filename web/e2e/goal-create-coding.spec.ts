import { expect, test } from "@playwright/test";
import { refreshAccessToken } from "./helpers";
import {
  controlForLabel,
  deleteGoalsByPrefix,
  fillGoalName,
  getGoalByName,
  gotoGoals,
  openNewGoal,
  pickOption,
  saveNewGoal,
  setTarget,
} from "./goals-helpers";

// goal-create-coding — the CODING half of "the predicate builder must work for
// coding AND reading". Author a plain time-on-axis coding goal through the real
// builder (pick an axis + value, a target, a window), save, and prove it both
// shows in the goals list AND round-trips to the backend as a coding-source
// (source-less) time leaf.
const GOAL_NAME = "E2E Coding Goal";

test.beforeEach(async ({ request }) => {
  const token = await refreshAccessToken(request);
  await deleteGoalsByPrefix(request, token, "E2E Coding Goal");
});

test("creates a coding-time goal via the predicate builder and persists it", async ({
  page,
  request,
}) => {
  await gotoGoals(page);

  const dialog = await openNewGoal(page);
  await fillGoalName(dialog, GOAL_NAME);

  // Default is the coding metric; assert it reads as selected and stay on it.
  await expect(dialog.getByTestId("metric-coding")).toHaveAttribute(
    "aria-pressed",
    "true",
  );

  // Pick axis=project, value=boomtime — the canonical "time on a project" goal.
  await pickOption(page, await controlForLabel(dialog, "Axis"), "project");
  await (await controlForLabel(dialog, "Value (blank = any)")).fill("boomtime");

  // Target 2h, weekly window (week is the default; set it explicitly anyway).
  await setTarget(dialog, "2h");
  await pickOption(page, await controlForLabel(dialog, "Window"), "week");

  await saveNewGoal(page, dialog);

  // Shows in the list (the name also appears in the nearness strip → first()).
  await expect(page.getByText(GOAL_NAME, { exact: true }).first()).toBeVisible();

  // Round-trips to the backend as a coding time leaf: kind time, the picked
  // axis/value/window, and NO reading source (coding is the source-less default).
  const token = await refreshAccessToken(request);
  const goal = await getGoalByName(request, token, GOAL_NAME);
  expect(goal, "coding goal should be persisted").not.toBeNull();
  expect(goal!.spec.kind).toBe("time");
  expect(goal!.spec.axis).toBe("project");
  expect(goal!.spec.value).toBe("boomtime");
  expect(goal!.spec.window).toBe("week");
  expect(goal!.spec.target_seconds).toBe(7200); // 2h
  expect(goal!.spec.source ?? "").not.toBe("reading");
});
