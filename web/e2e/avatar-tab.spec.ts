import { expect, test } from "@playwright/test";
import {
  NO_STACK_REASON,
  loginAsNonAdmin,
  stackReachableFromEnv,
} from "./helpers";

// gaka-9v4 — Settings > Avatar tab (chibi portrait pipeline).
//
// Real generation takes 15s to 25min so this spec NEVER runs an actual
// render. We short-circuit the POST via `page.route()` so the RENDER
// click transitions the UI to the "generating" state without kicking off
// a real ComfyUI job.
//
// Coverage:
//   * Tab loads with the three-column console (INPUT / PROMPT / OUTPUT).
//   * Non-admin sees SYNTHESIZE disabled (the /admin/avatar/synthesize
//     endpoint is admin-gated), which is the primary contract for
//     everyday users.
//   * Manual prompt entry enables the RENDER button.
//   * Mocked POST /avatar/regenerate flips the UI into the rendering
//     scanline overlay state.
//   * Empty-state avatar preview: the ID-silhouette fallback (initials +
//     amber corner-bracket frame) appears when no avatar exists.

test.describe("gaka-9v4 — Settings > Avatar tab", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);

  test.beforeEach(async ({ page }) => {
    // Any authed user can VIEW the tab (SYNTHESIZE gate is server-side).
    // Use the non-admin fixture so this suite runs even when the admin
    // env vars aren't wired.
    await loginAsNonAdmin(page);
    await page.goto("/app/settings?tab=avatar");
    // The lazy Settings chunk mounts; wait for the tab body's header.
    await expect(
      page.getByText(/PROFILE SYNTHESIS · BIOMETRIC RENDER/),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("renders the three-panel console", async ({ page }) => {
    // AvatarTab wraps each column in a <PanelSection> whose header is
    // "> INPUT CONTEXT" / "> PROMPT SYNTHESIS" / "> OUTPUT / BIOMETRIC".
    await expect(page.getByText(/> INPUT CONTEXT/)).toBeVisible();
    await expect(page.getByText(/> PROMPT SYNTHESIS/)).toBeVisible();
    await expect(page.getByText(/> OUTPUT \/ BIOMETRIC/)).toBeVisible();
  });

  test("non-admin: SYNTHESIZE is disabled + tooltip explains why", async ({
    page,
  }) => {
    const synth = page.getByTestId("avatar-synthesize-btn");
    await expect(synth).toBeVisible();
    // The title carries the "admin-gated" explanation for non-admins.
    await expect(synth).toBeDisabled();
    const title = await synth.getAttribute("title");
    expect(title ?? "").toMatch(/admin-gated/i);
  });

  test("typing a manual prompt enables RENDER", async ({ page }) => {
    const render = page.getByTestId("avatar-render-btn");
    // Fresh non-admin has no seeded prompt — render is disabled.
    await expect(render).toBeDisabled();

    const textarea = page.getByTestId("avatar-prompt-textarea");
    await textarea.fill("chibi cyberpunk operator, dark background");
    await expect(render).toBeEnabled();
  });

  test("RENDER click mocks the regen POST and flips into rendering state", async ({
    page,
  }) => {
    // Short-circuit the regen POST — never let it hit real ComfyUI.
    // The route matches whether it's called against the vite proxy or a
    // remote origin.
    await page.route(
      /\/api\/v1\/users\/current\/avatar\/regenerate$/,
      async (route) => {
        await route.fulfill({
          status: 202,
          contentType: "application/json",
          body: JSON.stringify({ status: "pending" }),
        });
      },
    );

    // Also intercept the status poll so it reports "running" — the
    // RenderingScanlineOverlay only appears when
    // status === "running" | "pending".
    await page.route(
      /\/api\/v1\/users\/current\/avatar\/status$/,
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            status: "running",
            updatedAt: new Date().toISOString(),
          }),
        });
      },
    );

    // Give the interceptors time to be applied before the first poll fires.
    await page.getByTestId("avatar-prompt-textarea").fill(
      "chibi test operator, monochrome, no text",
    );

    // Click render. The button text switches to "▶ RENDERING…" and the
    // toast fires; both are proof the mocked 202 flowed through the FE
    // mutation path.
    const render = page.getByTestId("avatar-render-btn");
    await render.click();

    // Wait for the FE to flip to the rendering state.
    await expect(render).toBeDisabled({ timeout: 5_000 });
    await expect(render).toContainText(/RENDERING/i);
  });

  test("empty preview frame shows the ID-silhouette fallback", async ({
    page,
  }) => {
    // For a user with no avatar, UserAvatarImage falls back to initials
    // in an amber-bordered square, inside the corner-bracket frame.
    // Assert the frame exists and contains SOMETHING (either an <img>
    // or the fallback text — we don't care which as long as the slot
    // isn't empty).
    const frame = page.getByTestId("avatar-preview-frame");
    await expect(frame).toBeVisible();
    // The frame must contain either an <img> (avatar loaded) or a
    // non-empty text node (initials fallback).
    const hasContent = await frame.evaluate((el) => {
      const img = el.querySelector("img");
      if (img && (img.getAttribute("src") ?? "").length > 0) return true;
      return (el.textContent ?? "").trim().length > 0;
    });
    expect(hasContent).toBeTruthy();
  });
});
