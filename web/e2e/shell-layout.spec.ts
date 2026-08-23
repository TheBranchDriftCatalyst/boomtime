import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import {
  ADMIN_PASSWORD,
  ADMIN_USERNAME,
  NO_ADMIN_CREDS_REASON,
  NO_STACK_REASON,
  loginAsAdmin,
  stackReachableFromEnv,
} from "./helpers";

// gaka-c26s — the shell must never push its own header out of the viewport.
//
// The bug this pins down: AppShellNoScroll's content column was a bare `1fr`,
// i.e. minmax(AUTO, 1fr). An `auto` floor means the track cannot shrink below
// its content's min-content width, so a wide child stretched the column past
// the viewport instead of overflowing inside it — and the shell's own
// overflow-hidden then clipped the excess WITHOUT a scrollbar. Everything
// living at the right end of the header (search, notifications, the avatar menu
// and the logout inside it) became unreachable: measured 209px of overshoot at
// 1512px, and the whole cluster gone by 1280px. Admin and Settings were the
// only routes affected, because they were the only ones hoisting a wide tab
// strip into the header via useHeaderSlot.
//
// A class-level unit test (AppShellNoScroll.test.tsx) pins the CSS token, but
// only a browser can prove the layout consequence — and only a browser catches
// a NEW wide thing appearing in the header later. That's what this spec is for.

/** Widths that matter: the two most common laptop viewports, plus a phone. */
const VIEWPORTS = [
  { name: "1280 laptop", width: 1280, height: 800 },
  { name: "1512 macbook-14", width: 1512, height: 900 },
  { name: "390 phone", width: 390, height: 844 },
];

/** Routes whose chrome is at risk. Admin + Settings own the tab strips; a
 *  domain page is included as the control — it never regressed, so a failure
 *  there means the shell itself broke rather than the strip. */
const ROUTES = ["/app/admin/users", "/app/settings", "/app/projects"];

interface ShellMetrics {
  viewport: number;
  headerRight: number;
  headerWidth: number;
  docScrollWidth: number;
  controlsRight: number;
}

async function measureShell(page: Page): Promise<ShellMetrics> {
  return page.evaluate(() => {
    const header = document.querySelector("header");
    if (!header) throw new Error("no header rendered");
    const rect = header.getBoundingClientRect();
    // The right-hand control cluster is the header's last child (search, bell,
    // devtools, avatar menu). Its right edge is what actually went off-screen.
    const controls = header.lastElementChild;
    return {
      viewport: window.innerWidth,
      headerRight: Math.round(rect.right),
      headerWidth: Math.round(rect.width),
      docScrollWidth: document.documentElement.scrollWidth,
      controlsRight: controls
        ? Math.round(controls.getBoundingClientRect().right)
        : Math.round(rect.right),
    };
  });
}

test.describe("gaka-c26s — shell never overflows its own viewport", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);

  for (const vp of VIEWPORTS) {
    for (const route of ROUTES) {
      test(`${route} fits at ${vp.name}`, async ({ page }) => {
        // /app/admin/* needs the allowlist; /app/settings and /app/projects
        // would work as any user, but one login keeps the matrix uniform.
        test.skip(
          !ADMIN_USERNAME || !ADMIN_PASSWORD,
          NO_ADMIN_CREDS_REASON,
        );
        await page.setViewportSize({ width: vp.width, height: vp.height });
        await loginAsAdmin(page);
        await page.goto(route);

        // Let the hoisted header slot mount — the strip is what used to blow
        // the track out, so measuring before it renders would pass falsely.
        await page.waitForSelector("header", { timeout: 10_000 });
        await expect
          .poll(async () => (await measureShell(page)).headerWidth, {
            timeout: 5_000,
          })
          .toBeGreaterThan(0);

        const m = await measureShell(page);

        // The header must end inside the viewport. Sub-pixel rounding gets a
        // 1px allowance; anything beyond that is a real overflow.
        expect(
          m.headerRight,
          `header overflows the viewport by ${m.headerRight - m.viewport}px`,
        ).toBeLessThanOrEqual(m.viewport + 1);

        // And the right-hand controls specifically must be reachable — this is
        // the user-visible failure (no logout, no search, no notifications).
        expect(
          m.controlsRight,
          `header controls are clipped ${m.controlsRight - m.viewport}px off-screen`,
        ).toBeLessThanOrEqual(m.viewport + 1);

        // The shell owns exactly one viewport: the document itself must never
        // gain a horizontal scroll. (It didn't even during the bug — the
        // overflow was CLIPPED, which is why it was unrecoverable.)
        expect(m.docScrollWidth).toBeLessThanOrEqual(m.viewport + 1);
      });
    }
  }

  // The tests above assert the CURRENT pages fit. They passed even with the
  // buggy `grid-cols-[auto_1fr]` restored, because moving the section nav out
  // of the header removed today's only wide header child — so on their own they
  // guard the symptom, not the cause.
  //
  // This one exercises the cause directly: inject a deliberately over-wide node
  // into the header (standing in for whatever chrome someone hoists next) and
  // assert the shell CONTAINS it. With an `auto` column floor the track stretches
  // to fit the child and drags the header's controls out of the clipped
  // viewport; with `minmax(0,…)` the track stays viewport-sized and the overflow
  // stays the child's problem. No page has to be wide for this to fail.
  test("contains an over-wide header child instead of stretching to fit it", async ({
    page,
  }) => {
    test.skip(!ADMIN_USERNAME || !ADMIN_PASSWORD, NO_ADMIN_CREDS_REASON);
    await page.setViewportSize({ width: 1280, height: 800 });
    await loginAsAdmin(page);
    await page.goto("/app/projects");
    await page.waitForSelector("header", { timeout: 10_000 });

    const before = await measureShell(page);
    expect(before.headerRight).toBeLessThanOrEqual(before.viewport + 1);

    await page.evaluate(() => {
      const header = document.querySelector("header");
      if (!header) throw new Error("no header");
      const hog = document.createElement("div");
      hog.dataset.testid = "width-hog";
      // Far wider than any viewport, and flex-none so it cannot be shrunk —
      // exactly the shape the grouped tab strip had.
      // Full height, so the overpaint hit-test below is meaningful.
      hog.style.cssText = "width:3000px;flex:none;height:100%;";
      // Into the header's SLOT wrapper — the flex-1 box that useHeaderSlot
      // fills, i.e. exactly where hoisted page chrome lands. `flex:none` makes
      // it unshrinkable, which is what the grouped tab strip's tabs were.
      // The wrapper is the header's second child (first is the mobile-only nav,
      // display:none here), and injecting inside it leaves the control cluster
      // as lastElementChild so measureShell still reads the controls.
      const slot = header.children[1];
      if (!slot) throw new Error("no header slot wrapper");
      slot.appendChild(hog);
    });

    const after = await measureShell(page);
    expect(
      after.headerRight,
      `an over-wide header child stretched the shell ${after.headerRight - after.viewport}px past the viewport`,
    ).toBeLessThanOrEqual(after.viewport + 1);
    expect(
      after.controlsRight,
      "an over-wide header child pushed the header controls off-screen",
    ).toBeLessThanOrEqual(after.viewport + 1);
  });
});
