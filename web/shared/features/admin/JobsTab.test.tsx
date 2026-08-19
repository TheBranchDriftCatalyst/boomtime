// JobsTab.test.tsx — the Admin > Jobs tab (gaka-hney). Non-tautological coverage
// of the three pieces that carry behaviour:
//   1. Run-a-reading-step panel: renders (and only renders) its triggers when
//      books_enabled; a click POSTs the matching endpoint + toasts the jobId.
//   2. Grouped-by-kind jobs table: each kind's header row shows its live queue
//      stats (running/max headroom, queued depth, failures, throughput); expand
//      loads that kind's runs, paginated in place; per-row + per-kind + tab-wide
//      log-clears hit the right endpoint (with a confirm() gate on the bulk ones).
//   3. Per-job log side panel: a row click streams ONLY that job's lines.
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
vi.mock("@shared/features/logs/useLogsSocket", () => ({
  useLogsSocket: () => ({ logs: logFixture, status: "open", clear: () => {} }),
}));

import { JobsTab } from "@shared/features/admin/JobsTab";
import { authStore } from "@shared/features/auth/auth";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";

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

// A queue-overview row (drives a kind's header). Sensible defaults; override per test.
function queue(over: Record<string, unknown>) {
  return {
    kind: "hardcover-match",
    queued: 0,
    running: 0,
    maxConcurrency: 1,
    doneLastHour: 0,
    failedLastHour: 0,
    avgDurationMs: 0,
    lastRunAt: new Date().toISOString(),
    lastStatus: "done",
    ...over,
  };
}

// A jobs-table row for the per-kind /admin/jobs stub.
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

// Stub the three read endpoints the tab polls: queue overview (group headers),
// schedules (kept empty), and the per-kind jobs list (kind-filtered from the
// query string).
function stubReads(queues: unknown[], rowsForKind: (kind: string) => unknown[]) {
  server.use(
    http.get("/api/v1/admin/jobs/queues", () => HttpResponse.json({ queues })),
    http.get("/api/v1/admin/jobs/schedules", () => HttpResponse.json({ schedules: [] })),
    http.get("/api/v1/admin/jobs", ({ request }) => {
      const kind = new URL(request.url).searchParams.get("kind") ?? "";
      return HttpResponse.json({ jobs: rowsForKind(kind) });
    }),
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
  vi.restoreAllMocks();
});

describe("JobsTab — Run a reading step panel", () => {
  beforeEach(() => stubReads([], () => []));

  it("renders the 4 reading triggers when books_enabled", async () => {
    config(true);
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
    renderWithProviders(<JobsTab />);

    // The Schedules panel still renders — the tab isn't broken.
    await waitFor(() => expect(screen.getByText(/schedules/i)).toBeInTheDocument());
    expect(screen.queryByText(/run a reading step/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /hardcover match/i })).not.toBeInTheDocument();
  });

  it("triggers Kindle backfill → POST /kindle/backfill + toast with jobId", async () => {
    config(true);
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
});

describe("JobsTab — grouped kind headers", () => {
  const now = new Date().toISOString();

  it("renders one header per kind with headroom, depth, failures and throughput", async () => {
    config(false);
    stubReads(
      [
        // at cap WITH a backlog → pacing; 1 failure in the trailing hour.
        queue({
          kind: "hardcover-match",
          queued: 3,
          running: 1,
          maxConcurrency: 1,
          doneLastHour: 2,
          failedLastHour: 1,
          lastRunAt: now,
          lastStatus: "running",
        }),
        // idle, well under cap, no failures.
        queue({
          kind: "github-stats-refresh",
          queued: 0,
          running: 0,
          maxConcurrency: 2,
          doneLastHour: 5,
          failedLastHour: 0,
          lastRunAt: now,
          lastStatus: "done",
        }),
      ],
      () => [],
    );

    renderWithProviders(<JobsTab />);

    const group = await screen.findByTestId("job-group-hardcover-match");
    // Headroom label = running/max, bar is amber at cap.
    expect(group.textContent).toContain("1/1");
    expect(screen.getByTestId("job-group-bar-hardcover-match").innerHTML).toContain(
      "bg-amber-500",
    );
    // At cap WITH a backlog = pacing back-pressure + the queued depth.
    expect(group.textContent).toContain("pacing");
    expect(group.textContent).toContain("3 queued");
    // Throughput + failures (warn color on the fail chip).
    expect(group.textContent).toContain("2/h");
    expect(group.textContent).toContain("1 failed");
    expect(screen.getByTestId("job-group-fail-hardcover-match").className).toContain(
      "text-destructive",
    );
    // Failing takes precedence for the state dot — a kind with recent failures
    // reads red even while it's running.
    expect(screen.getByTestId("job-group-dot-hardcover-match").className).toContain(
      "bg-destructive",
    );

    // The idle kind renders too: under-cap bar not amber, no fail color, muted dot.
    const idle = screen.getByTestId("job-group-github-stats-refresh");
    expect(idle.textContent).toContain("0/2");
    expect(screen.getByTestId("job-group-bar-github-stats-refresh").innerHTML).not.toContain(
      "bg-amber-500",
    );
    expect(screen.getByTestId("job-group-fail-github-stats-refresh").className).not.toContain(
      "text-destructive",
    );
    expect(screen.getByTestId("job-group-dot-github-stats-refresh").className).toContain(
      "bg-muted-foreground",
    );

    // Collapsed by design: no run rows are fetched/shown until a kind is expanded.
    expect(screen.queryByRole("row")).not.toBeInTheDocument();
  });

  it("shows 'at cap' (not 'pacing') when running==max with no backlog", async () => {
    config(false);
    stubReads(
      [queue({ kind: "avatar-render", queued: 0, running: 1, maxConcurrency: 1, lastStatus: "running" })],
      () => [],
    );

    renderWithProviders(<JobsTab />);

    const group = await screen.findByTestId("job-group-avatar-render");
    expect(group.textContent).toContain("at cap");
    expect(group.textContent).not.toContain("pacing");
    // Running with no failures → accent (primary) state dot.
    expect(screen.getByTestId("job-group-dot-avatar-render").className).toContain("bg-primary");
  });
});

describe("JobsTab — expand a kind and paginate its runs", () => {
  it("loads the kind's runs on expand and pages them 8 at a time", async () => {
    config(false);
    // 10 runs of the kind → 2 pages of PAGE_SIZE=8.
    const runs = Array.from({ length: 10 }, (_, i) =>
      jobRow({ id: i + 1, kind: "hardcover-match", status: "done", finishedAt: new Date().toISOString() }),
    );
    stubReads(
      [queue({ kind: "hardcover-match", running: 0, doneLastHour: 10, lastStatus: "done" })],
      (kind) => (kind === "hardcover-match" ? runs : []),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    // Expand the group via its header toggle.
    await user.click(await screen.findByTitle(/expand hardcover-match/i));

    // Page 1 shows the first 8 rows (ids 1..8), not 9/10.
    await waitFor(() => expect(screen.getByText("Page 1 / 2")).toBeInTheDocument());
    expect(screen.getByRole("cell", { name: "1" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "8" })).toBeInTheDocument();
    expect(screen.queryByRole("cell", { name: "9" })).not.toBeInTheDocument();

    // Next → page 2 reveals the tail (id 9), page 1 rows gone.
    await user.click(screen.getByRole("button", { name: /next/i }));
    await waitFor(() => expect(screen.getByText("Page 2 / 2")).toBeInTheDocument());
    expect(screen.getByRole("cell", { name: "9" })).toBeInTheDocument();
    expect(screen.queryByRole("cell", { name: "1" })).not.toBeInTheDocument();
  });
});

describe("JobsTab — per-row actions inside an expanded kind", () => {
  async function expandAndFind(row: Record<string, unknown>) {
    const runs = [jobRow(row)];
    stubReads(
      [queue({ kind: "hardcover-match", running: 1, lastStatus: "running" })],
      (kind) => (kind === "hardcover-match" ? runs : []),
    );
    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);
    await user.click(await screen.findByTitle(/expand hardcover-match/i));
    return user;
  }

  it("clicking a run row opens the panel and streams only that job's logs", async () => {
    config(false);
    const user = await expandAndFind({ id: 7, kind: "hardcover-match", status: "running" });

    // Row → open the side panel (click its id cell).
    await user.click(await screen.findByRole("cell", { name: "7" }));

    const panel = await screen.findByRole("dialog");
    expect(within(panel).getByText("hardcover-match")).toBeInTheDocument();
    expect(within(panel).getByText("#7")).toBeInTheDocument();
    // Only the job_id=7 lines show; job_id=99 is filtered out.
    expect(within(panel).getByText(/jobs: started/)).toBeInTheDocument();
    expect(within(panel).getByText(/matched three books/)).toBeInTheDocument();
    expect(within(panel).queryByText(/line for a different job/)).not.toBeInTheDocument();
  });

  it("per-row clear → DELETE /jobs/:id/logs, no panel opened", async () => {
    config(false);
    let clearHits = 0;
    server.use(
      http.delete("/api/v1/admin/jobs/7/logs", () => {
        clearHits += 1;
        return HttpResponse.json({ deleted: true });
      }),
    );
    const user = await expandAndFind({ id: 7, kind: "hardcover-match", status: "done", finishedAt: new Date().toISOString() });

    await user.click(await screen.findByRole("button", { name: /clear logs for job 7/i }));
    await waitFor(() => expect(clearHits).toBe(1));
    // stopPropagation: the row's log panel must NOT have opened.
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Cleared stored logs for job #7"),
    );
  });

  it("cancel on a running row → POST cancel, no panel opened", async () => {
    config(false);
    let cancelHits = 0;
    server.use(
      http.post("/api/v1/admin/jobs/7/cancel", () => {
        cancelHits += 1;
        return HttpResponse.json({ cancelled: true, wasRunning: true });
      }),
    );
    const user = await expandAndFind({ id: 7, kind: "hardcover-match", status: "running" });

    await user.click(await screen.findByRole("button", { name: /cancel job #7/i }));
    await waitFor(() => expect(cancelHits).toBe(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("retry on a failed row → POST retry, no panel opened", async () => {
    config(false);
    let retryHits = 0;
    server.use(
      http.post("/api/v1/admin/jobs/7/retry", () => {
        retryHits += 1;
        return HttpResponse.json({ id: 7 });
      }),
    );
    const user = await expandAndFind({
      id: 7,
      kind: "hardcover-match",
      status: "failed",
      error: "boom",
      finishedAt: new Date().toISOString(),
    });

    await user.click(await screen.findByRole("button", { name: /re-enqueue job #7/i }));
    await waitFor(() => expect(retryHits).toBe(1));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("JobsTab — bulk log clears (confirm-gated)", () => {
  it("Clear all logs → confirm → DELETE /jobs/logs (no kind)", async () => {
    config(false);
    stubReads([queue({ kind: "hardcover-match" })], () => []);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    let deleteUrl: string | null = null;
    server.use(
      http.delete("/api/v1/admin/jobs/logs", ({ request }) => {
        deleteUrl = request.url;
        return HttpResponse.json({ deleted: 4 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByRole("button", { name: /clear all logs/i }));
    await waitFor(() => expect(deleteUrl).not.toBeNull());
    expect(new URL(deleteUrl!).searchParams.get("kind")).toBeNull();
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Cleared 4 stored logs"));
  });

  it("Clear-all does nothing when the confirm() is declined", async () => {
    config(false);
    stubReads([queue({ kind: "hardcover-match" })], () => []);
    vi.spyOn(window, "confirm").mockReturnValue(false);
    let hits = 0;
    server.use(
      http.delete("/api/v1/admin/jobs/logs", () => {
        hits += 1;
        return HttpResponse.json({ deleted: 0 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);
    await user.click(await screen.findByRole("button", { name: /clear all logs/i }));
    // Give any (erroneous) request a chance to fire, then assert none did.
    await new Promise((r) => setTimeout(r, 50));
    expect(hits).toBe(0);
  });

  it("Clear-kind → confirm → DELETE /jobs/logs?kind=<kind>", async () => {
    config(false);
    stubReads([queue({ kind: "hardcover-match" })], () => []);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    let deleteUrl: string | null = null;
    server.use(
      http.delete("/api/v1/admin/jobs/logs", ({ request }) => {
        deleteUrl = request.url;
        return HttpResponse.json({ deleted: 2 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobsTab />);

    await user.click(await screen.findByRole("button", { name: /clear hardcover-match logs/i }));
    await waitFor(() => expect(deleteUrl).not.toBeNull());
    expect(new URL(deleteUrl!).searchParams.get("kind")).toBe("hardcover-match");
  });
});
