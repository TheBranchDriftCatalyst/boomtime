// Helpers for the reading-surfaces + cross-domain goal-authoring Playwright
// suite (goal-create-*, predicate-builder-domains, reading-dashboard,
// books-explore).
//
// Additive: these live alongside the existing add-to-space / recent-features
// helpers and reuse the SAME storageState-preauthenticated browser context (the
// e2e-playwright-user captured in global-setup). Nothing here logs a different
// identity in — every spec that imports this drives the app as that one seeded
// user, exactly like the add-to-space suite.
import type { APIRequestContext, Locator, Page } from "@playwright/test";
import { expect } from "@playwright/test";
import { BASE_URL } from "./consts";

// ---------------------------------------------------------------------------
// Goals API (Basic-token, mirrors seedHeartbeats / listSpaces in helpers.ts).
// The goals endpoints live under /api/v1/users/current/goals and accept the
// same `Authorization: Basic <accessToken>` the heartbeats.bulk seeder uses.
// ---------------------------------------------------------------------------

/** Minimal shape of a persisted goal — only the fields the specs assert on. */
export interface ApiGoal {
  id: string;
  name: string;
  description?: string;
  // The predicate tree. Kept as `any` here: each spec narrows the arm it cares
  // about (a top-level time leaf, or an all/any group) itself.
  spec: any;
  public: boolean;
  enabled: boolean;
}

/** GET every goal for the e2e user. The endpoint wraps as `{ goals: [...] }`. */
export async function listGoals(
  request: APIRequestContext,
  token: string,
): Promise<ApiGoal[]> {
  const res = await request.get(`${BASE_URL}/api/v1/users/current/goals`, {
    headers: { Authorization: `Basic ${token}` },
  });
  if (!res.ok()) throw new Error(`list goals failed: ${res.status()}`);
  return ((await res.json()).goals ?? []) as ApiGoal[];
}

/** The single goal with this exact name, or null. */
export async function getGoalByName(
  request: APIRequestContext,
  token: string,
  name: string,
): Promise<ApiGoal | null> {
  const goals = await listGoals(request, token);
  return goals.find((g) => g.name === name) ?? null;
}

/** Delete every goal whose name starts with `prefix` — keeps re-runs idempotent. */
export async function deleteGoalsByPrefix(
  request: APIRequestContext,
  token: string,
  prefix: string,
): Promise<void> {
  for (const g of await listGoals(request, token)) {
    if (g.name.startsWith(prefix)) {
      await request.delete(
        `${BASE_URL}/api/v1/users/current/goals/${encodeURIComponent(g.id)}`,
        { headers: { Authorization: `Basic ${token}` } },
      );
    }
  }
}

// ---------------------------------------------------------------------------
// Predicate-builder UI helpers.
//
// The builder is a Radix-Select-heavy recursive form (see PredicateBuilder.tsx).
// Each control's <Label htmlFor> points at the Select trigger's useId() id — but
// React's useId() emits ids containing ":" which are illegal in a bare `#id` CSS
// selector, so we resolve the label's `for` and match it with an [id="…"]
// attribute selector instead.
// ---------------------------------------------------------------------------

/** The trigger/input associated with a field <Label> by its text, within `scope`. */
export async function controlForLabel(
  scope: Locator,
  labelText: string,
): Promise<Locator> {
  const forId = await scope
    .locator("label", { hasText: labelText })
    .first()
    .getAttribute("for");
  if (!forId) throw new Error(`label "${labelText}" has no htmlFor target`);
  return scope.locator(`[id="${forId}"]`);
}

/** Open a Radix Select `trigger` and click the option with `optionName`. */
export async function pickOption(
  page: Page,
  trigger: Locator,
  optionName: string,
): Promise<void> {
  await trigger.click();
  // Options render in a body-level portal, so query the page, not the scope.
  await page.getByRole("option", { name: optionName, exact: true }).click();
}

/**
 * The KindSwitcher combobox has no label; it displays its current kind
 * (KIND_LABELS: "Time on axis", "All of (AND)", …). Target it by that text.
 */
export function kindSwitcher(scope: Locator, currentLabel: string): Locator {
  return scope.getByRole("combobox").filter({ hasText: currentLabel }).first();
}

/** Fill a DurationInput (Target field) and commit it with Enter. */
export async function setTarget(scope: Locator, text: string): Promise<void> {
  const input = await controlForLabel(scope, "Target");
  await input.fill(text);
  await input.press("Enter");
}

// ---------------------------------------------------------------------------
// Navigation / form open+save.
// ---------------------------------------------------------------------------

/** Go to /app/goals (storageState-authed) and assert we weren't bounced. */
export async function gotoGoals(page: Page): Promise<void> {
  await page.goto("/app/goals");
  await expect(page).toHaveURL(/\/app\/goals/);
}

/** Open the create-goal modal and return the dialog locator. */
export async function openNewGoal(page: Page): Promise<Locator> {
  // Two "New goal" buttons can coexist: the tab-header action and the
  // empty-state CTA (shown when the list is empty). Either opens the same
  // modal; take the header one (first in DOM order).
  await page.getByRole("button", { name: "New goal" }).first().click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  return dialog;
}

/** Type a goal name into the open create/edit dialog. */
export async function fillGoalName(dialog: Locator, name: string): Promise<void> {
  await dialog.locator("#goal-name").fill(name);
}

/** Click "Create goal" and wait for the modal to close (success). */
export async function saveNewGoal(page: Page, dialog: Locator): Promise<void> {
  await dialog.getByRole("button", { name: "Create goal" }).click();
  await expect(dialog).toBeHidden();
}

/** Click "Save changes" (edit mode) and wait for the modal to close. */
export async function saveGoalEdits(page: Page, dialog: Locator): Promise<void> {
  await dialog.getByRole("button", { name: "Save changes" }).click();
  await expect(dialog).toBeHidden();
}
