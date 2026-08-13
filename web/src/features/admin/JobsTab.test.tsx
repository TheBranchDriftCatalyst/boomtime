// JobsTab.test.tsx — the "Run a reading step" panel (on-demand catalyst-books
// pipeline triggers, scoped to the current admin user). Non-tautological:
//   1. The panel renders all 4 triggers ONLY when books_enabled.
//   2. It renders nothing (but the rest of the tab still works) when books off.
//   3. Clicking a trigger POSTs the matching endpoint + toasts the jobId.
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

import { JobsTab } from "@/features/admin/JobsTab";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

function config(books: boolean) {
  server.use(
    http.get("/api/v1/config/public", () =>
      HttpResponse.json({
        registration_enabled: true,
        auth_provider: "local",
        oidc_enabled: false,
        billing_enabled: false,
        beta_flags: {},
        github_connect_enabled: false,
        books_enabled: books,
      }),
    ),
  );
}

// Keep the Schedules + Jobs panels quiet so the test focuses on the reading panel.
function stubJobsEndpoints() {
  server.use(
    http.get("/api/v1/admin/jobs/schedules", () => HttpResponse.json({ schedules: [] })),
    http.get("/api/v1/admin/jobs", () => HttpResponse.json({ jobs: [] })),
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

describe("JobsTab — Run a reading step panel", () => {
  it("renders the 4 reading triggers when books_enabled", async () => {
    config(true);
    stubJobsEndpoints();
    renderWithProviders(<JobsTab />);

    await waitFor(() =>
      expect(screen.getByText(/run a reading step/i)).toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: /audible backfill/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /kindle backfill/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /hardcover match/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /hardcover pull/i })).toBeInTheDocument();
  });

  it("hides the reading panel when books are disabled (rest of tab intact)", async () => {
    config(false);
    stubJobsEndpoints();
    renderWithProviders(<JobsTab />);

    // The Schedules panel still renders — the tab isn't broken.
    await waitFor(() => expect(screen.getByText(/schedules/i)).toBeInTheDocument());
    expect(screen.queryByText(/run a reading step/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /hardcover match/i })).not.toBeInTheDocument();
  });

  it("triggers Kindle backfill → POST /kindle/backfill + toast with jobId", async () => {
    config(true);
    stubJobsEndpoints();
    let hit = 0;
    server.use(
      http.post("/api/v1/kindle/backfill", () => {
        hit += 1;
        return HttpResponse.json({ enqueued: true, jobId: 91 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByRole("button", { name: /kindle backfill/i }));
    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Kindle backfill started (job #91)"),
    );
  });

  it("triggers Hardcover pull → POST /hardcover/pull + toast with jobId", async () => {
    config(true);
    stubJobsEndpoints();
    let hit = 0;
    server.use(
      http.post("/api/v1/hardcover/pull", () => {
        hit += 1;
        return HttpResponse.json({ enqueued: true, jobId: 5 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByRole("button", { name: /hardcover pull/i }));
    await waitFor(() => expect(hit).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Hardcover pull started (job #5)"),
    );
  });
});
