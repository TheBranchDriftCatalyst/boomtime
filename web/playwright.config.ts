import { defineConfig, devices } from "@playwright/test";

// e2e for the add-to-Space heartbeat-explorer feature. The dev stack (Go
// backend :8080 + Vite dev server :5173) runs in docker; there is no
// `webServer` here — global-setup polls both and fails with a clear message if
// they are down.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [["list"]],
  timeout: 60_000,
  expect: { timeout: 10_000 },

  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",

  use: {
    baseURL: "http://localhost:5173",
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },

  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // Boot the browser already authenticated via the captured refresh cookie.
        storageState: "e2e/.auth/state.json",
      },
    },
  ],
});
