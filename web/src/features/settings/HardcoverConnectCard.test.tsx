// HardcoverConnectCard.test.tsx — the read-only "Match books" / "Pull from
// Hardcover" trigger row. Non-tautological coverage:
//   1. The trigger row only renders once Hardcover is connected.
//   2. "Match books" POSTs /api/v1/hardcover/match + toasts the jobId.
//   3. "Pull from Hardcover" POSTs /api/v1/hardcover/pull + toasts the jobId.
//   4. The safe/read-only caption is present.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

import { HardcoverConnectCard } from "@/features/settings/HardcoverConnectCard";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

function enableBooks() {
  server.use(
    http.get("/api/v1/config/public", () =>
      HttpResponse.json({
        registration_enabled: true,
        auth_provider: "local",
        oidc_enabled: false,
        billing_enabled: false,
        beta_flags: {},
        github_connect_enabled: false,
        books_enabled: true,
      }),
    ),
  );
}

function hardcover(connected: boolean, status = "valid") {
  server.use(
    http.get("/api/v1/hardcover", () =>
      HttpResponse.json(connected ? { connected: true, status } : { connected: false }),
    ),
  );
}

beforeEach(() => {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
  toastSuccess.mockClear();
  toastError.mockClear();
});

afterEach(() => {
  authStore.clear();
});

describe("HardcoverConnectCard — match/pull triggers", () => {
  it("does not render the trigger row until Hardcover is connected", async () => {
    enableBooks();
    hardcover(false);
    renderWithProviders(<HardcoverConnectCard />);

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^connect$/i })).toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /match books/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /pull from hardcover/i })).not.toBeInTheDocument();
  });

  it("renders the safe/read-only caption when connected", async () => {
    enableBooks();
    hardcover(true);
    renderWithProviders(<HardcoverConnectCard />);

    await screen.findByRole("button", { name: /match books/i });
    expect(screen.getByText(/read-only and safe/i)).toBeInTheDocument();
    expect(screen.getByText(/dry-run-gated/i)).toBeInTheDocument();
  });

  it("matches books → POST /hardcover/match + toast with jobId", async () => {
    enableBooks();
    hardcover(true);
    let hit = 0;
    server.use(
      http.post("/api/v1/hardcover/match", () => {
        hit += 1;
        return HttpResponse.json({ enqueued: true, jobId: 12 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<HardcoverConnectCard />);

    await user.click(await screen.findByRole("button", { name: /match books/i }));
    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Hardcover match started (job #12)"),
    );
  });

  it("pulls from Hardcover → POST /hardcover/pull + toast with jobId", async () => {
    enableBooks();
    hardcover(true);
    let hit = 0;
    server.use(
      http.post("/api/v1/hardcover/pull", () => {
        hit += 1;
        return HttpResponse.json({ enqueued: true, jobId: 34 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<HardcoverConnectCard />);

    await user.click(await screen.findByRole("button", { name: /pull from hardcover/i }));
    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Hardcover pull started (job #34)"),
    );
  });
});
