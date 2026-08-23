// TimezoneCard.test.tsx (boom-dg7) — non-tautological tests for the Settings
// > Profile timezone card.
//
// Coverage:
//   1. Renders the effective TZ from the server payload.
//   2. Manual pick + Save PATCHes with the selected IANA name.
//   3. Auto-detect fires when user has no explicit pick AND browser TZ
//      differs from the server's effective TZ.
//   4. Auto-detect NOOPs when the user already has an explicit pick.
//   5. Auto-detect NOOPs when browser TZ == effective TZ (would be a wasted
//      round-trip).
//
// The tests stub Intl.DateTimeFormat().resolvedOptions().timeZone so the
// auto-detect logic is deterministic across CI runners in different zones.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TimezoneCard } from "@shared/features/settings/TimezoneCard";
import { authStore } from "@shared/features/auth/auth";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";

const toastCalls: string[] = [];
const toastErrorCalls: string[] = [];
vi.mock("sonner", () => {
  // sonner's toast API is `toast(message)` for neutral + `toast.error(message)`
  // for errors. Both channels are captured so the "Detected timezone" toast
  // can be asserted separately from the error toast.
  const toast = ((m: string) => {
    toastCalls.push(m);
  }) as ((m: string) => void) & { error: (m: string) => void; success: (m: string) => void };
  toast.error = (m: string) => {
    toastErrorCalls.push(m);
  };
  toast.success = (m: string) => {
    toastCalls.push(m);
  };
  return { toast };
});

// stubBrowserTZ: overrides Intl.DateTimeFormat().resolvedOptions().timeZone
// for the duration of a test. Restored in afterEach.
let originalResolvedOptions:
  | typeof Intl.DateTimeFormat.prototype.resolvedOptions
  | undefined;
function stubBrowserTZ(zone: string) {
  originalResolvedOptions ??= Intl.DateTimeFormat.prototype.resolvedOptions;
  const orig = originalResolvedOptions;
  Intl.DateTimeFormat.prototype.resolvedOptions = function () {
    return { ...orig.call(this), timeZone: zone };
  };
}

// stubSupportedZones: force listSupportedZones() to return a known small set
// so the test's assertions about auto-detect (which requires the browser
// zone to be in the list) are deterministic regardless of the runtime's
// Intl.supportedValuesOf implementation. Restored in afterEach.
let originalSupportedValuesOf:
  | ((key: string) => string[])
  | undefined
  | null; // undefined = never set; null = was absent (delete)
function stubSupportedZones(zones: string[]) {
  const IntlAny = Intl as unknown as {
    supportedValuesOf?: (key: string) => string[];
  };
  if (originalSupportedValuesOf === undefined) {
    originalSupportedValuesOf =
      IntlAny.supportedValuesOf === undefined ? null : IntlAny.supportedValuesOf;
  }
  IntlAny.supportedValuesOf = () => zones;
}

beforeEach(() => {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
  toastCalls.length = 0;
  toastErrorCalls.length = 0;
});

afterEach(() => {
  if (originalResolvedOptions) {
    Intl.DateTimeFormat.prototype.resolvedOptions = originalResolvedOptions;
    originalResolvedOptions = undefined;
  }
  if (originalSupportedValuesOf !== undefined) {
    const IntlAny = Intl as unknown as {
      supportedValuesOf?: (key: string) => string[];
    };
    if (originalSupportedValuesOf === null) {
      delete IntlAny.supportedValuesOf;
    } else {
      IntlAny.supportedValuesOf = originalSupportedValuesOf;
    }
    originalSupportedValuesOf = undefined;
  }
});

describe("TimezoneCard (boom-dg7)", () => {
  it("renders the effective timezone from the server payload", async () => {
    // Fresh account: no explicit pick, server default resolves to PT.
    server.use(
      http.get("/api/v1/users/current/timezone", () =>
        HttpResponse.json({
          timezone: "",
          effectiveTimezone: "America/Los_Angeles",
        }),
      ),
    );
    // Match the browser zone to the server's effective TZ so no auto-detect
    // fires — this test asserts pure rendering.
    stubBrowserTZ("America/Los_Angeles");
    stubSupportedZones([
      "UTC",
      "America/Los_Angeles",
      "America/New_York",
      "Europe/Paris",
    ]);

    renderWithProviders(<TimezoneCard />);

    // The effective TZ + hint render inside the card header. Wait for the
    // query to resolve — before that lands, the fallback text is "UTC" from
    // the `data?.effectiveTimezone ?? "UTC"` guard.
    await waitFor(() =>
      expect(screen.getByTestId("timezone-effective")).toHaveTextContent(
        "America/Los_Angeles",
      ),
    );
    // "server default" hint appears because raw is '' but effective is non-UTC.
    expect(screen.getByTestId("timezone-hint")).toHaveTextContent(
      "server default",
    );
  });

  it("manual dropdown pick + Save PATCHes with the correct value", async () => {
    // User has an explicit pick already so no auto-detect fires; they're
    // switching from PT to ET.
    server.use(
      http.get("/api/v1/users/current/timezone", () =>
        HttpResponse.json({
          timezone: "America/Los_Angeles",
          effectiveTimezone: "America/Los_Angeles",
        }),
      ),
    );
    stubBrowserTZ("America/Los_Angeles");
    stubSupportedZones([
      "UTC",
      "America/Los_Angeles",
      "America/New_York",
      "Europe/Paris",
    ]);

    const captured: { body: unknown } = { body: undefined };
    server.use(
      http.patch("/api/v1/users/current/timezone", async ({ request }) => {
        captured.body = await request.json();
        return HttpResponse.json({
          timezone: "America/New_York",
          effectiveTimezone: "America/New_York",
        });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<TimezoneCard />);

    // Wait for the initial load.
    await screen.findByTestId("timezone-effective");

    const select = (await screen.findByTestId(
      "timezone-select",
    )) as HTMLSelectElement;
    await user.selectOptions(select, "America/New_York");

    const saveBtn = screen.getByTestId("timezone-save");
    await user.click(saveBtn);

    await waitFor(() =>
      expect(captured.body).toEqual({ timezone: "America/New_York" }),
    );
  });

  it("auto-detects on mount when user has no explicit pick and browser TZ differs from effective TZ", async () => {
    // User never picked; server default is UTC (fallback). Browser reports
    // America/New_York — auto-detect MUST fire.
    server.use(
      http.get("/api/v1/users/current/timezone", () =>
        HttpResponse.json({ timezone: "", effectiveTimezone: "UTC" }),
      ),
    );
    stubBrowserTZ("America/New_York");
    stubSupportedZones([
      "UTC",
      "America/Los_Angeles",
      "America/New_York",
      "Europe/Paris",
    ]);

    const captured: { body: unknown; count: number } = {
      body: undefined,
      count: 0,
    };
    server.use(
      http.patch("/api/v1/users/current/timezone", async ({ request }) => {
        captured.count += 1;
        captured.body = await request.json();
        return HttpResponse.json({
          timezone: "America/New_York",
          effectiveTimezone: "America/New_York",
        });
      }),
    );

    renderWithProviders(<TimezoneCard />);

    // Auto-detect PATCH fires exactly once.
    await waitFor(() => expect(captured.count).toBe(1));
    expect(captured.body).toEqual({ timezone: "America/New_York" });
    // Neutral toast surfaces the detection.
    expect(toastCalls).toContain("Detected timezone: America/New_York");
  });

  it("auto-detect NOOPs when the user already has an explicit pick", async () => {
    // User already picked PT. Browser is ET — we should NOT auto-overwrite
    // their choice.
    server.use(
      http.get("/api/v1/users/current/timezone", () =>
        HttpResponse.json({
          timezone: "America/Los_Angeles",
          effectiveTimezone: "America/Los_Angeles",
        }),
      ),
    );
    stubBrowserTZ("America/New_York");
    stubSupportedZones([
      "UTC",
      "America/Los_Angeles",
      "America/New_York",
    ]);

    const captured = { count: 0 };
    server.use(
      http.patch("/api/v1/users/current/timezone", async () => {
        captured.count += 1;
        return HttpResponse.json({
          timezone: "America/New_York",
          effectiveTimezone: "America/New_York",
        });
      }),
    );

    renderWithProviders(<TimezoneCard />);

    // Wait for the GET to land + a brief window for effects to flush.
    await screen.findByTestId("timezone-effective");
    // Force a tick — if auto-detect were going to fire, it would by now.
    await new Promise((r) => setTimeout(r, 20));

    expect(captured.count).toBe(0);
    expect(toastCalls).not.toContain(
      "Detected timezone: America/New_York",
    );
  });

  it("auto-detect NOOPs when browser TZ == effective TZ (would be a no-op PATCH)", async () => {
    // No explicit pick but effective TZ already matches the browser. Firing
    // a PATCH would be wasted work; the guard has to hold.
    server.use(
      http.get("/api/v1/users/current/timezone", () =>
        HttpResponse.json({
          timezone: "",
          effectiveTimezone: "America/Los_Angeles",
        }),
      ),
    );
    stubBrowserTZ("America/Los_Angeles");
    stubSupportedZones([
      "UTC",
      "America/Los_Angeles",
      "America/New_York",
    ]);

    const captured = { count: 0 };
    server.use(
      http.patch("/api/v1/users/current/timezone", async () => {
        captured.count += 1;
        return HttpResponse.json({
          timezone: "America/Los_Angeles",
          effectiveTimezone: "America/Los_Angeles",
        });
      }),
    );

    renderWithProviders(<TimezoneCard />);

    await screen.findByTestId("timezone-effective");
    await new Promise((r) => setTimeout(r, 20));

    expect(captured.count).toBe(0);
  });
});
