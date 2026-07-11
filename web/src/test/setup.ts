import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll, beforeEach } from "vitest";
import { cleanup } from "@testing-library/react";
import { server } from "@/test/msw/server";
import { authStore } from "@/features/auth/auth";

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
