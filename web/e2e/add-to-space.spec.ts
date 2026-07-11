import { expect, test } from "@playwright/test";
import { E2E_SPACE_NAME, E2E_TARGET_PROJECT } from "./consts";
import {
  addSpaceRule,
  createSpace,
  deleteSpacesByPrefix,
  refreshAccessToken,
} from "./helpers";

/**
 * Add-to-Space heartbeat-explorer feature (commit 76a9901).
 *
 * Flow: navigate to the Heartbeats explorer, find the seeded target project's
 * top-level group row, use its hover-revealed "Add to Space" dropdown, and
 * assert the sky-blue Space membership badge appears and links to
 * /app/space/:id. Auth comes from the storageState captured in global-setup.
 */
test("adds a project group to a Space and shows the linking badge", async ({
  page,
}) => {
  await page.goto("/app/heartbeats");

  // Authenticated: we must NOT be bounced to /login. If we are, storageState /
  // refresh bootstrap is broken — fix setup, do not weaken this assertion.
  await expect(page).toHaveURL(/\/app\/heartbeats/);

  // The explorer renders group rows once the root query resolves. Group rows
  // are <tr> containing the project value text.
  const targetRow = page
    .locator("tr", { hasText: E2E_TARGET_PROJECT })
    .first();
  await expect(targetRow).toBeVisible({ timeout: 15_000 });

  // Hover to reveal the row actions (the trigger is opacity-0 until hover).
  await targetRow.hover();

  // The "Add to Space" trigger: <button title='Add Project "<value>" to a Space'>.
  const addTrigger = targetRow.getByRole("button", {
    name: `Add Project "${E2E_TARGET_PROJECT}" to a Space`,
  });
  await expect(addTrigger).toBeVisible();
  await addTrigger.click();

  // The dropdown opens with an "Add to Space" label and one item per Space.
  await expect(
    page.getByRole("menu").getByText("Add to Space"),
  ).toBeVisible();
  await page.getByRole("menuitem", { name: E2E_SPACE_NAME }).click();

  // Optional: the sonner success toast confirms the write.
  await expect(
    page.getByText(`Added "${E2E_TARGET_PROJECT}" to ${E2E_SPACE_NAME}`),
  ).toBeVisible({ timeout: 10_000 });

  // Success: the sky-blue Space badge appears on the row — a link whose title
  // is `In Space "<name>" — open it` containing the space name. The link's
  // accessible name is its text (the space name), so target the title attr.
  const spaceBadge = targetRow.locator(
    `a[title='In Space "${E2E_SPACE_NAME}" — open it']`,
  );
  await expect(spaceBadge).toBeVisible({ timeout: 10_000 });
  await expect(spaceBadge).toContainText(E2E_SPACE_NAME);

  // The badge links correctly: clicking navigates to the Space view.
  await spaceBadge.click();
  await expect(page).toHaveURL(/\/app\/space\/\d+/);
  // The Space view renders its (editable) name heading.
  await expect(page.getByText(E2E_SPACE_NAME).first()).toBeVisible({
    timeout: 10_000,
  });
});

/**
 * Regex membership badging (the real-world scenario: Spaces are usually defined
 * by a regex rule, e.g. `project ~ ^catalyst`, not by exact per-value rules).
 * A Space whose regex rule matches the row's value must badge that row WITHOUT
 * any manual "add" — proving useSpaceMembership evaluates regex rules
 * client-side, not just exact ones.
 */
test("badges a project row from a REGEX Space rule, with no manual add", async ({
  page,
  request,
}) => {
  const regexSpaceName = `${E2E_SPACE_NAME} (regex)`;
  const token = await refreshAccessToken(request);

  // Idempotent on retry: drop any prior copy, then create the Space + a regex
  // rule that matches the seeded target project (`^e2e-space` ⊇ e2e-space-target).
  await deleteSpacesByPrefix(request, token, regexSpaceName);
  const space = await createSpace(request, token, regexSpaceName);
  await addSpaceRule(request, token, space.id, {
    axis: "project",
    matchValue: "^e2e-space",
    matchType: "regex",
  });

  await page.goto("/app/heartbeats");
  await expect(page).toHaveURL(/\/app\/heartbeats/);

  const targetRow = page
    .locator("tr", { hasText: E2E_TARGET_PROJECT })
    .first();
  await expect(targetRow).toBeVisible({ timeout: 15_000 });

  // The regex-matched Space badge is present on load — no dropdown, no add.
  const regexBadge = targetRow.locator(
    `a[title='In Space "${regexSpaceName}" — open it']`,
  );
  await expect(regexBadge).toBeVisible({ timeout: 10_000 });
  await expect(regexBadge).toContainText(regexSpaceName);
});

/**
 * Two Spaces claiming the same row: when a value belongs to more than one Space
 * (here one via a regex rule, one via an exact rule), the row must render a
 * distinct badge for EACH — the per-Space dedup collapses duplicate rules
 * within a Space, not across Spaces.
 */
test("shows one badge per Space when two Spaces match the same row", async ({
  page,
  request,
}) => {
  const nameA = `${E2E_SPACE_NAME} Multi-A`;
  const nameB = `${E2E_SPACE_NAME} Multi-B`;
  const token = await refreshAccessToken(request);

  // Idempotent on retry.
  await deleteSpacesByPrefix(request, token, nameA);
  await deleteSpacesByPrefix(request, token, nameB);

  // Space A matches via regex; Space B matches the same value via an exact rule.
  const spaceA = await createSpace(request, token, nameA);
  await addSpaceRule(request, token, spaceA.id, {
    axis: "project",
    matchValue: "^e2e-space",
    matchType: "regex",
  });
  const spaceB = await createSpace(request, token, nameB);
  await addSpaceRule(request, token, spaceB.id, {
    axis: "project",
    matchValue: E2E_TARGET_PROJECT,
    matchType: "exact",
  });

  await page.goto("/app/heartbeats");
  await expect(page).toHaveURL(/\/app\/heartbeats/);

  const targetRow = page
    .locator("tr", { hasText: E2E_TARGET_PROJECT })
    .first();
  await expect(targetRow).toBeVisible({ timeout: 15_000 });

  // Both badges are present on the one row.
  await expect(
    targetRow.locator(`a[title='In Space "${nameA}" — open it']`),
  ).toBeVisible({ timeout: 10_000 });
  await expect(
    targetRow.locator(`a[title='In Space "${nameB}" — open it']`),
  ).toBeVisible();
});
