import { expect, test } from "@playwright/test";
import { refreshAccessToken } from "./helpers";
import {
  controlForLabel,
  deleteGoalsByPrefix,
  fillGoalName,
  getGoalByName,
  gotoGoals,
  kindSwitcher,
  openNewGoal,
  pickOption,
  saveNewGoal,
} from "./goals-helpers";

// predicate-builder-domains — the cross-domain builder proper: a nested group
// (all → any) holding ONE leaf per domain (a reading listening leaf + a coding
// time leaf), asserting each leaf keeps its own domain/source through a group
// kind switch and round-trips that way to the backend. This is the structural
// heart of "the builder must work for coding AND reading, together".
const GOAL_NAME = "E2E Mixed Domains Goal";

test.beforeEach(async ({ request }) => {
  const token = await refreshAccessToken(request);
  await deleteGoalsByPrefix(request, token, "E2E Mixed Domains Goal");
});

test("composes a group with a reading leaf and a coding leaf, each keeping its domain", async ({
  page,
  request,
}) => {
  await gotoGoals(page);

  const dialog = await openNewGoal(page);
  await fillGoalName(dialog, GOAL_NAME);

  // Start reading, then wrap into an OR group. The reading child is preserved
  // via seedLeaf → readingLeafOf. NB: switching a group's kind reseeds it to a
  // single leaf (convertKind), so we pick the final operator BEFORE adding the
  // second condition, not after.
  await dialog.getByTestId("metric-listening").click();
  await pickOption(page, kindSwitcher(dialog, "Time on axis"), "Any of (OR)");

  // Append a second condition — a fresh coding (source-less) time leaf.
  await dialog.getByRole("button", { name: "+ Add condition" }).click();

  // One reading leaf (metric chip) + one coding leaf (Axis select) now coexist,
  // each keeping its own domain.
  await expect(dialog.getByTestId("reading-metric-label")).toHaveCount(1);
  await expect(dialog.locator("label", { hasText: "Axis" })).toHaveCount(1);

  // Make the coding leaf concrete: language=Go. Its domain is unaffected by the
  // reading sibling.
  await pickOption(page, await controlForLabel(dialog, "Axis"), "language");
  await (await controlForLabel(dialog, "Value (blank = any)")).fill("Go");

  // Both leaves still hold their domains after editing the coding sibling.
  await expect(dialog.getByTestId("reading-metric-label")).toHaveCount(1);
  await expect(dialog.locator("label", { hasText: "Axis" })).toHaveCount(1);

  await saveNewGoal(page, dialog);
  await expect(page.getByText(GOAL_NAME, { exact: true }).first()).toBeVisible();

  // Round-trips as an `any` group of two time leaves: exactly one reading-source
  // leaf and one coding (source-less, axis=language) leaf.
  const token = await refreshAccessToken(request);
  const goal = await getGoalByName(request, token, GOAL_NAME);
  expect(goal, "mixed-domain goal should be persisted").not.toBeNull();
  expect(goal!.spec.kind).toBe("any");
  const of = goal!.spec.of as any[];
  expect(of).toHaveLength(2);

  const reading = of.find((n) => n.source === "reading");
  const coding = of.find((n) => (n.source ?? "") !== "reading");
  expect(reading, "a reading-source leaf survived").toBeTruthy();
  expect(coding, "a coding leaf survived").toBeTruthy();
  expect(coding.kind).toBe("time");
  expect(coding.axis).toBe("language");
  expect(coding.value).toBe("Go");
});
