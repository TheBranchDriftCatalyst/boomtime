// AmazonConnectCard.test.tsx — the Kindle trigger row (shared Amazon device
// feeds BOTH Audible + Kindle). Non-tautological coverage:
//   1. The Kindle row only renders once Amazon is connected.
//   2. "Backfill Kindle library" POSTs /api/v1/kindle/backfill and toasts the jobId.
//   3. "Sync Kindle" POSTs /api/v1/kindle/sync and toasts the synced count.
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

import { AmazonConnectCard } from "@/features/settings/AmazonConnectCard";
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

function amazon(connected: boolean) {
  server.use(
    http.get("/api/v1/amazon", () => HttpResponse.json({ connected })),
    http.get("/api/v1/books/items", () => HttpResponse.json({ items: [] })),
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

describe("AmazonConnectCard — Kindle triggers", () => {
  it("does not render the Kindle row until Amazon is connected", async () => {
    enableBooks();
    amazon(false);
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /connect amazon/i })).toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /backfill kindle library/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /sync kindle/i })).not.toBeInTheDocument();
  });

  it("backfills Kindle → POST /kindle/backfill + toast with jobId", async () => {
    enableBooks();
    amazon(true);
    let hit = 0;
    server.use(
      http.post("/api/v1/kindle/backfill", () => {
        hit += 1;
        return HttpResponse.json({ enqueued: true, jobId: 77 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    const btn = await screen.findByRole("button", { name: /backfill kindle library/i });
    await user.click(btn);

    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Kindle backfill started (job #77)"),
    );
  });

  it("syncs Kindle → POST /kindle/sync + toast with count", async () => {
    enableBooks();
    amazon(true);
    let hit = 0;
    server.use(
      http.post("/api/v1/kindle/sync", () => {
        hit += 1;
        return HttpResponse.json({ synced: 3, source: "kindle" });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    const btn = await screen.findByRole("button", { name: /sync kindle/i });
    await user.click(btn);

    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Synced 3 Kindle items"));
  });

  it("surfaces the ApiError message as an error toast on failure", async () => {
    enableBooks();
    amazon(true);
    server.use(
      http.post("/api/v1/kindle/backfill", () =>
        HttpResponse.json({ message: "device offline" }, { status: 502 }),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<AmazonConnectCard />, { withRouter: true });

    await user.click(await screen.findByRole("button", { name: /backfill kindle library/i }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("device offline"));
    expect(toastSuccess).not.toHaveBeenCalled();
  });
});
