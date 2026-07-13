import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StartImportForm } from "@/features/import/StartImportForm";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

// The double-base64 bug that motivates this file:
//   FE btoa(token) -> BE base64(base64(token)) -> Basic header decodes to
//   base64 garbage -> wakatime.com returns 401 on the very first
//   /user_agents fetch. See gaka-f2l. These tests lock the contract that
//   the wakatime api_key is sent OVER THE WIRE VERBATIM.

const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    error: (m: string) => toastError(m),
    success: (m: string) => toastSuccess(m),
    info: (m: string) => toastSuccess(m),
  },
}));

// Realistic-shape wakatime.com key (waka_ prefix + UUID). If our FE btoa's it
// the request body value would collapse to "d2FrYV8..." and this assertion
// fails — that is exactly the regression this file guards.
const RAW_WAKA_KEY = "waka_11111111-2222-3333-4444-555555555555";

function stubConfig({ hasServerKey }: { hasServerKey: boolean }) {
  server.use(
    http.get("/import/config", () =>
      HttpResponse.json({ hasServerKey }),
    ),
    http.get("/api/v1/users/current/heartbeats/latest", () =>
      HttpResponse.json({ lastHeartbeat: null, count: 0 }),
    ),
  );
}

function captureSubmit() {
  const body: { current: unknown } = { current: undefined };
  server.use(
    http.post("/import", async ({ request }) => {
      body.current = await request.json();
      return HttpResponse.json({ jobId: 42, jobStatus: "queued" });
    }),
  );
  return body;
}

function captureDetect() {
  const body: { current: unknown } = { current: undefined };
  server.use(
    http.post("/import/wakatime-range", async ({ request }) => {
      body.current = await request.json();
      return HttpResponse.json({
        startDate: "2025-01-01",
        endDate: "2025-06-01",
        totalSeconds: 3600,
        text: "5 mos",
        hasData: true,
      });
    }),
  );
  return body;
}

describe("StartImportForm — wakatime api_key wire contract (gaka-f2l)", () => {
  it("submits the raw api_key verbatim (no client-side base64)", async () => {
    stubConfig({ hasServerKey: false });
    const captured = captureSubmit();
    const user = userEvent.setup();

    renderWithProviders(<StartImportForm onStarted={() => {}} />);

    await user.type(screen.getByLabelText(/Wakatime API token/i), RAW_WAKA_KEY);
    await user.click(screen.getByRole("button", { name: /start import/i }));

    await waitFor(() => expect(captured.current).toBeDefined());
    const req = captured.current as { apiToken?: string };
    // The raw key must land as-is; server does the single Basic base64-encode.
    expect(req.apiToken).toBe(RAW_WAKA_KEY);
    // Belt: guard against the specific double-encode that reproduced the 401.
    expect(req.apiToken).not.toBe(btoa(RAW_WAKA_KEY));
  });

  it("sends the raw api_key on Detect range too", async () => {
    stubConfig({ hasServerKey: false });
    const captured = captureDetect();
    const user = userEvent.setup();

    renderWithProviders(<StartImportForm onStarted={() => {}} />);

    await user.type(screen.getByLabelText(/Wakatime API token/i), RAW_WAKA_KEY);
    await user.click(screen.getByRole("button", { name: /detect range/i }));

    await waitFor(() => expect(captured.current).toBeDefined());
    const req = captured.current as { apiToken?: string };
    expect(req.apiToken).toBe(RAW_WAKA_KEY);
    expect(req.apiToken).not.toBe(btoa(RAW_WAKA_KEY));
  });

  it("omits apiToken from the submit body when the field is blank", async () => {
    // When the server has a key configured, the FE lets the user skip
    // pasting a token and fall back to the env key. The submit body must
    // NOT carry an apiToken at all in that case (server treats the empty
    // string as an override and would fail).
    stubConfig({ hasServerKey: true });
    const captured = captureSubmit();
    const user = userEvent.setup();

    renderWithProviders(<StartImportForm onStarted={() => {}} />);

    await user.click(screen.getByRole("button", { name: /start import/i }));

    await waitFor(() => expect(captured.current).toBeDefined());
    const req = captured.current as Record<string, unknown>;
    expect("apiToken" in req).toBe(false);
  });
});

describe("StartImportForm — password visibility toggle", () => {
  it("toggles the token input between password and text", async () => {
    stubConfig({ hasServerKey: false });
    const user = userEvent.setup();

    renderWithProviders(<StartImportForm onStarted={() => {}} />);

    const input = screen.getByLabelText(/Wakatime API token/i);
    expect(input).toHaveAttribute("type", "password");

    const toggle = screen.getByRole("button", { name: /show token/i });
    await user.click(toggle);
    expect(input).toHaveAttribute("type", "text");

    // Now the toggle button flips its label.
    const hide = screen.getByRole("button", { name: /hide token/i });
    await user.click(hide);
    expect(input).toHaveAttribute("type", "password");
  });
});
