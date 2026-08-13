// JobsTab.test.tsx — the "Run a reading step" panel (on-demand catalyst-books
// pipeline triggers, scoped to the current admin user). Non-tautological:
//   1. The panel renders all 4 triggers ONLY when books_enabled.
//   2. It renders nothing (but the rest of the tab still works) when books off.
//   3. Clicking a trigger POSTs the matching endpoint + toasts the jobId.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

// The per-job side panel subscribes to the shared server log stream via
// useLogsSocket. Mock it to yield a fixed set of entries: two tagged job_id=7
// and one tagged job_id=99 — the panel must show only the job it was opened
// for. (vi.hoisted so the fixture is available inside the hoisted vi.mock.)
const { logFixture } = vi.hoisted(() => ({
  logFixture: [
    {
      id: 1,
      time: "2026-07-10T00:00:00Z",
      level: "INFO",
      msg: "jobs: started",
      attrs: { job_id: "7", kind: "hardcover-match" },
      source: "worker",
    },
    {
      id: 2,
      time: "2026-07-10T00:00:01Z",
      level: "INFO",
      msg: "matched three books",
      attrs: { job_id: "7", count: "3" },
      source: "worker",
    },
    {
      id: 3,
      time: "2026-07-10T00:00:02Z",
      level: "INFO",
      msg: "line for a different job",
      attrs: { job_id: "99" },
      source: "worker",
    },
  ],
}));
vi.mock("@/features/logs/useLogsSocket", () => ({
  useLogsSocket: () => ({ logs: logFixture, status: "open", clear: () => {} }),
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

// A job table row shape for the /admin/jobs stub.
function jobRow(over: Record<string, unknown>) {
  const now = new Date().toISOString();
  return {
    id: 1,
    kind: "hardcover-match",
    status: "running",
    attempts: 1,
    maxAttempts: 1,
    error: "",
    runAt: now,
    createdAt: now,
    startedAt: now,
    finishedAt: null,
    ...over,
  };
}

describe("JobsTab — cancel a running job", () => {
  it("shows Cancel on a running row, POSTs cancel, and refetches the list", async () => {
    config(false); // keep the reading panel out of the way
    server.use(
      http.get("/api/v1/admin/jobs/schedules", () => HttpResponse.json({ schedules: [] })),
    );
    let listHits = 0;
    let cancelHits = 0;
    server.use(
      http.get("/api/v1/admin/jobs", () => {
        listHits += 1;
        return HttpResponse.json({ jobs: [jobRow({ id: 7, status: "running" })] });
      }),
      http.post("/api/v1/admin/jobs/7/cancel", () => {
        cancelHits += 1;
        return HttpResponse.json({ cancelled: true, wasRunning: true });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    const cancelBtn = await screen.findByRole("button", { name: /cancel/i });
    expect(cancelBtn).toBeInTheDocument();
    const before = listHits;

    await user.click(cancelBtn);
    await waitFor(() => expect(cancelHits).toBe(1));
    // Invalidating the jobs prefix refetches the table.
    await waitFor(() => expect(listHits).toBeGreaterThan(before));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Cancelling job #7…"),
    );
  });

  it("offers no Cancel on a terminal (done) row", async () => {
    config(false);
    server.use(
      http.get("/api/v1/admin/jobs/schedules", () => HttpResponse.json({ schedules: [] })),
      http.get("/api/v1/admin/jobs", () =>
        HttpResponse.json({
          jobs: [jobRow({ id: 8, kind: "hardcover-pull", status: "done", finishedAt: new Date().toISOString() })],
        }),
      ),
    );

    renderWithProviders(<JobsTab />);

    await waitFor(() => expect(screen.getByText("hardcover-pull")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /cancel/i })).not.toBeInTheDocument();
  });
});

describe("JobsTab — per-job log side panel", () => {
  beforeEach(() => {
    config(false); // keep the reading panel out of the way
    server.use(
      http.get("/api/v1/admin/jobs/schedules", () => HttpResponse.json({ schedules: [] })),
    );
  });

  it("clicking a row opens the panel and streams only that job's logs", async () => {
    server.use(
      http.get("/api/v1/admin/jobs", () =>
        HttpResponse.json({ jobs: [jobRow({ id: 7, kind: "hardcover-match", status: "running" })] }),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    // Click the row (via its kind cell) to open the panel.
    await user.click(await screen.findByText("hardcover-match"));

    const panel = await screen.findByRole("dialog");
    // Header restates the job identity: kind + #id.
    expect(within(panel).getByText("hardcover-match")).toBeInTheDocument();
    expect(within(panel).getByText("#7")).toBeInTheDocument();

    // Only the job_id=7 lines show; the job_id=99 line is filtered out.
    expect(within(panel).getByText(/jobs: started/)).toBeInTheDocument();
    expect(within(panel).getByText(/matched three books/)).toBeInTheDocument();
    expect(within(panel).queryByText(/line for a different job/)).not.toBeInTheDocument();
  });

  it("shows the empty-state when the job has no captured logs", async () => {
    server.use(
      http.get("/api/v1/admin/jobs", () =>
        // id 42 has no matching log lines in the fixture (only 7 + 99 exist).
        HttpResponse.json({ jobs: [jobRow({ id: 42, kind: "github-stats-refresh", status: "queued" })] }),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByText("github-stats-refresh"));

    const panel = await screen.findByRole("dialog");
    expect(within(panel).getByText(/no logs yet for this job/i)).toBeInTheDocument();
  });

  it("clicking Retry does NOT open the panel (stopPropagation)", async () => {
    let retryHits = 0;
    server.use(
      http.get("/api/v1/admin/jobs", () =>
        HttpResponse.json({ jobs: [jobRow({ id: 7, kind: "hardcover-match", status: "failed", error: "boom" })] }),
      ),
      http.post("/api/v1/admin/jobs/7/retry", () => {
        retryHits += 1;
        return HttpResponse.json({ id: 7 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByRole("button", { name: /retry/i }));
    await waitFor(() => expect(retryHits).toBe(1));
    // The row's onClick must not have fired: no log panel opened.
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("clicking Cancel does NOT open the panel (stopPropagation)", async () => {
    let cancelHits = 0;
    server.use(
      http.get("/api/v1/admin/jobs", () =>
        HttpResponse.json({ jobs: [jobRow({ id: 7, kind: "hardcover-match", status: "running" })] }),
      ),
      http.post("/api/v1/admin/jobs/7/cancel", () => {
        cancelHits += 1;
        return HttpResponse.json({ cancelled: true, wasRunning: true });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByRole("button", { name: /cancel/i }));
    await waitFor(() => expect(cancelHits).toBe(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
