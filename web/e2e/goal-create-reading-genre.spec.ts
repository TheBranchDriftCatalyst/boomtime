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

// goal-create-reading-genre — the reading leaf's second shape (boom-dvy9): a
// reading dimension goal. Pick the "Listening time (Audible)" metric, switch its
// Filter dropdown to Genre, set the value to "Fiction", save → it persists as a
// reading-source, genre-dimensioned time leaf (runtime of finished books by
// genre).
const GOAL_NAME = "E2E Reading Genre Goal";

test.beforeEach(async ({ request }) => {
  const token = await refreshAccessToken(request);
  await deleteGoalsByPrefix(request, token, "E2E Reading Genre Goal");
});

test("creates a genre-filtered reading goal and persists the dimension", async ({
  page,
  request,
}) => {
  await gotoGoals(page);

  const dialog = await openNewGoal(page);
  await fillGoalName(dialog, GOAL_NAME);

  await dialog.getByTestId("metric-listening").click();
  await expect(dialog.getByTestId("reading-metric-label")).toBeVisible();

  // Switch the reading Filter from "Total listening" to Genre; the free-text
  // value input then appears.
  await pickOption(page, await controlForLabel(dialog, "Filter"), "Genre");
  const value = dialog.getByTestId("reading-dimension-value");
  await expect(value).toBeVisible();
  await value.fill("Fiction");

  await setTarget(dialog, "4h");
  await saveNewGoal(page, dialog);

  await expect(page.getByText(GOAL_NAME, { exact: true }).first()).toBeVisible();

  // Persisted as a reading-source, genre="Fiction" time leaf.
  const token = await refreshAccessToken(request);
  const goal = await getGoalByName(request, token, GOAL_NAME);
  expect(goal, "genre reading goal should be persisted").not.toBeNull();
  expect(goal!.spec.kind).toBe("time");
  expect(goal!.spec.source).toBe("reading");
  expect(goal!.spec.axis).toBe("genre");
  expect(goal!.spec.value).toBe("Fiction");
  expect(goal!.spec.target_seconds).toBe(14400); // 4h
});
