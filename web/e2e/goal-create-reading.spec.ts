import { expect, test } from "@playwright/test";
import { refreshAccessToken } from "./helpers";
import {
  deleteGoalsByPrefix,
  fillGoalName,
  getGoalByName,
  gotoGoals,
  kindSwitcher,
  openNewGoal,
  pickOption,
  saveGoalEdits,
  saveNewGoal,
  setTarget,
} from "./goals-helpers";

// goal-create-reading — the READING half of the cross-domain builder, plus the
// gaka-bs5l regression guard: a reading leaf must STAY reading through a
// KindSwitcher round-trip (wrap in a group, unwrap) AND through a later edit —
// it must never silently revert to a coding axis.
const GOAL_NAME = "E2E Reading Goal";

test.beforeEach(async ({ request }) => {
  const token = await refreshAccessToken(request);
  await deleteGoalsByPrefix(request, token, "E2E Reading Goal");
});

test("creates a Listening-time reading goal that survives a Kind round-trip", async ({
  page,
  request,
}) => {
  await gotoGoals(page);

  const dialog = await openNewGoal(page);
  await fillGoalName(dialog, GOAL_NAME);

  // Seed the whole spec from the "Listening time (Audible)" metric.
  await dialog.getByTestId("metric-listening").click();
  await expect(dialog.getByTestId("metric-listening")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  // The reading total-listening leaf shows its read-only metric chip, not a
  // coding Axis select.
  await expect(dialog.getByTestId("reading-metric-label")).toBeVisible();
  await expect(dialog.locator("label", { hasText: "Axis" })).toHaveCount(0);

  await setTarget(dialog, "3h");

  // gaka-bs5l: wrap the reading leaf in an AND group, then unwrap back to a
  // single Time leaf. It MUST remain reading (recovered via readingLeafOf), not
  // fall back to the coding default.
  await pickOption(page, kindSwitcher(dialog, "Time on axis"), "All of (AND)");
  await expect(dialog.getByTestId("reading-metric-label")).toBeVisible(); // child stayed reading
  await pickOption(page, kindSwitcher(dialog, "All of (AND)"), "Time on axis");

  await expect(dialog.getByTestId("reading-metric-label")).toBeVisible();
  await expect(dialog.locator("label", { hasText: "Axis" })).toHaveCount(0);
  // The quick-start reflects the reconstructed spec: still the reading metric.
  await expect(dialog.getByTestId("metric-listening")).toHaveAttribute(
    "aria-pressed",
    "true",
  );

  await saveNewGoal(page, dialog);
  await expect(page.getByText(GOAL_NAME, { exact: true }).first()).toBeVisible();

  // Persisted as a reading-source weekly time leaf, 3h target.
  let token = await refreshAccessToken(request);
  let goal = await getGoalByName(request, token, GOAL_NAME);
  expect(goal, "reading goal should be persisted").not.toBeNull();
  expect(goal!.spec.kind).toBe("time");
  expect(goal!.spec.source).toBe("reading");
  expect(goal!.spec.window).toBe("week");
  expect(goal!.spec.target_seconds).toBe(10800); // 3h

  // STAYS reading through an edit: reopen, change only the target, save.
  await page.getByRole("button", { name: "Edit goal" }).first().click();
  const editDialog = page.getByRole("dialog");
  await expect(editDialog).toBeVisible();
  // Edit mode hides the quick-start buttons but the reading leaf itself must
  // still render as reading (metric chip, no coding Axis).
  await expect(editDialog.getByTestId("reading-metric-label")).toBeVisible();
  await expect(editDialog.locator("label", { hasText: "Axis" })).toHaveCount(0);
  await setTarget(editDialog, "4h");
  await saveGoalEdits(page, editDialog);

  token = await refreshAccessToken(request);
  goal = await getGoalByName(request, token, GOAL_NAME);
  expect(goal!.spec.source).toBe("reading"); // did NOT revert to coding
  expect(goal!.spec.target_seconds).toBe(14400); // 4h
});
