import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { cleanup } from "@testing-library/react";
import { server } from "@shared/test/msw/server";
import { authStore } from "@shared/features/auth/auth";
import { registerHostDomains } from "@shared/app/registerDomains";

// Populate the nav / settings / admin registration seams the same way the host
// app entry does, so components that read them (Sidebar, Settings, AdminPage)
// render their full domain-grouped surface under test. Idempotent.
registerHostDomains();

// --- msw lifecycle -----------------------------------------------------------
beforeAll(() =>
  // Error on any request that isn't explicitly handled, so accidental network
  // calls fail loudly instead of hanging.
  server.listen({ onUnhandledRequest: "error" }),
);
afterEach(() => {
  cleanup();
  server.resetHandlers();
  // Reset shared global state between tests (zero cross-test bleed).
  authStore.clear();
  try {
    window.localStorage.clear();
  } catch {
    /* ignore */
  }
});
afterAll(() => server.close());

// --- matchMedia polyfill (jsdom lacks it; ThemeProvider/system theme use it) -
beforeEach(() => {
  if (!window.matchMedia) {
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: (query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: () => {},
        removeEventListener: () => {},
        addListener: () => {},
        removeListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  }
});

// jsdom lacks scrollIntoView (used by the Projects selector).
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

// jsdom lacks ResizeObserver (used by grid WidgetHost's useMeasuredSize
// and viz/d3/useChartFrame). Shim as a no-op: tests can't measure the
// DOM anyway so an observer that never fires is the honest answer.
// Without this, any test that mounts a grid tile crashes inside
// commitHookLayoutEffects, blanking the render tree.
type ResizeObserverInit = {
  observe: (target: Element) => void;
  unobserve: (target: Element) => void;
  disconnect: () => void;
};
if (typeof (globalThis as { ResizeObserver?: unknown }).ResizeObserver === "undefined") {
  (globalThis as { ResizeObserver?: unknown }).ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  } as unknown as ResizeObserverInit;
}

// jsdom lacks Element.prototype.hasPointerCapture / setPointerCapture /
// releasePointerCapture. Radix UI's Select (and other pointer-capture
// primitives — Slider, Toggle, Popover triggers) call these under
// userEvent's pointerdown flow. Without the shim, tests that click a
// Radix trigger fail with "target.hasPointerCapture is not a function".
// Shim as no-ops (return false for hasPointerCapture — jsdom never
// captures pointers, so the answer is genuinely "no").
// boom-wpb.1 (audit): the goals PredicateBuilder tests were the first
// to trip on this; the "convert leaf to group" test had to be
// tautologized to work around the missing shim. Adding it here lets
// that test drive the real DOM instead.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
}
if (!Element.prototype.setPointerCapture) {
  Element.prototype.setPointerCapture = () => {};
}
if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = () => {};
}
