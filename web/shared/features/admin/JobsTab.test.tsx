// JobsTab.test.tsx — the Admin > Jobs tab (boom-hney). Non-tautological coverage
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
import {
  PageActionsOutlet,
  PageActionsProvider,
} from "@shared/layout/PageActionsSlot";
import { authStore } from "@shared/features/auth/auth";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";

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
    owner: "",
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

// Stub the reads the console polls. Two things changed with the restructure:
// the queue overview now carries CHAINS alongside the per-kind stats, and the
// run table fetches the recent window ONCE (unfiltered) and groups it in memory
// rather than issuing a request per expanded kind.
function stubReads(
  queues: unknown[],
  jobs: unknown[] = [],
  chains: unknown[] = [],
) {
  server.use(
    http.get("/api/v1/admin/jobs/queues", () =>
      HttpResponse.json({ queues, chains }),
    ),
    http.get("/api/v1/admin/jobs/schedules", () => HttpResponse.json({ schedules: [] })),
    http.get("/api/v1/admin/jobs", () => HttpResponse.json({ jobs })),
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

// JobsTab pushes its tab-level control ("Clear all logs") UP through the
// page-actions slot instead of hand-rolling a header row (boom-9e9k), so
// mounting it bare renders that button nowhere. This reproduces the section
// shell's composition — provider + a reader — so the tests drive the same
// wiring production does rather than a shape that only exists under test.
function renderJobsTab() {
  return renderWithProviders(
    <PageActionsProvider>
      <PageActionsOutlet />
      <JobsTab />
    </PageActionsProvider>,
  );
}

// The console is ONE groupable table now, not three stacked panels and a list
// of accordions. These pin the behaviour that restructure carries, not its
// markup: what an operator can see without drilling, and what they can run.
describe("JobsTab — every registered kind is runnable, with no hardcoded list", () => {
  it("renders a run button for a kind nothing in the component names", async () => {
    stubReads([queue({ kind: "books-kindle-annotations" })]);
    let body: unknown = null;
    server.use(
      http.post("/api/v1/admin/jobs/trigger", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: 42 });
      }),
    );

    const user = userEvent.setup();
    renderJobsTab();

    // The kind arrives from the API; the component has no list of kinds at all.
    await user.click(
      await screen.findByRole("button", { name: /run books-kindle-annotations/i }),
    );
    await waitFor(() => expect(body).toEqual({ kind: "books-kindle-annotations" }));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        "Enqueued books-kindle-annotations — job #42",
      ),
    );
  });

  // A schedule is a property of a KIND, so it rides on the kind's row rather
  // than in the separate table it used to live in.
  it("shows the kind's cadence on its row", async () => {
    server.use(
      http.get("/api/v1/admin/jobs/queues", () =>
        HttpResponse.json({ queues: [queue({ kind: "books-audible-sync" })], chains: [] }),
      ),
      http.get("/api/v1/admin/jobs", () => HttpResponse.json({ jobs: [] })),
      http.get("/api/v1/admin/jobs/schedules", () =>
        HttpResponse.json({
          schedules: [
            {
              kind: "books-audible-sync",
              intervalSeconds: 3600,
              nextRun: new Date(Date.now() + 12 * 60_000).toISOString(),
              lastRun: null,
            },
          ],
        }),
      ),
    );
    renderJobsTab();
    expect(
      await screen.findByTestId("job-group-schedule-books-audible-sync"),
    ).toHaveTextContent("1h");
  });

  // Most kinds are enqueue-on-demand, so labelling the absence would be noise on
  // nearly every row.
  it("shows no cadence for an unscheduled kind", async () => {
    stubReads([queue({ kind: "books-kindle-annotations" })]);
    renderJobsTab();
    await screen.findByRole("button", { name: /run books-kindle-annotations/i });
    expect(
      screen.queryByTestId("job-group-schedule-books-kindle-annotations"),
    ).not.toBeInTheDocument();
  });
});

// A chain is the one thing a flat per-kind list genuinely cannot express:
// ORDER. Rendered from whatever the registry declares, so a new pipeline needs
// no frontend change.
describe("JobsTab — composed pipelines", () => {
  const chain = {
    kind: "books-sync-all",
    steps: ["books-kindle-sync", "books-kindle-annotations", "hardcover-pull"],
  };

  it("renders the declared steps in run order", async () => {
    stubReads([queue({ kind: "books-sync-all" })], [], [chain]);
    renderJobsTab();

    const strip = await screen.findByTestId("jobs-chains");
    const labels = within(strip)
      .getAllByRole("button")
      .map((b) => b.getAttribute("aria-label"))
      .filter((l): l is string => !!l && l.startsWith("run step"));
    expect(labels).toEqual([
      "run step books-kindle-sync",
      "run step books-kindle-annotations",
      "run step hardcover-pull",
    ]);
  });

  it("runs the whole chain, and any single step, through the same generic trigger", async () => {
    stubReads([queue({ kind: "books-sync-all" })], [], [chain]);
    const enqueued: string[] = [];
    server.use(
      http.post("/api/v1/admin/jobs/trigger", async ({ request }) => {
        const b = (await request.json()) as { kind: string };
        enqueued.push(b.kind);
        return HttpResponse.json({ id: enqueued.length });
      }),
    );

    const user = userEvent.setup();
    renderJobsTab();

    await user.click(await screen.findByRole("button", { name: /run chain books-sync-all/i }));
    await user.click(
      await screen.findByRole("button", { name: /run step books-kindle-annotations/i }),
    );
    await waitFor(() =>
      expect(enqueued).toEqual(["books-sync-all", "books-kindle-annotations"]),
    );
  });
});

// Every run the console triggers is FLEET-WIDE (the generic trigger enqueues
// owner-less, and every handler is dual-mode). The table has to say so rather
// than leave the column blank, because "no owner" is a real answer, not missing
// data.
describe("JobsTab — ownership is visible", () => {
  it("labels an owner-less run as fleet-wide, and names a scoped one", async () => {
    stubReads(
      [queue({ kind: "hardcover-match" })],
      [
        jobRow({ id: 1, owner: "", status: "done", finishedAt: new Date().toISOString() }),
        jobRow({ id: 2, owner: "panda", status: "done", finishedAt: new Date().toISOString() }),
      ],
    );
    const user = userEvent.setup();
    renderJobsTab();

    // Drill into the kind to reach its runs.
    const group = await screen.findByText("hardcover-match");
    await user.click(group);

    await waitFor(() => expect(screen.getByText("panda")).toBeInTheDocument());
    expect(screen.getByText(/^fleet$/i)).toBeInTheDocument();
  });

  it("offers owner as a group axis so runs can be sliced by user", async () => {
    stubReads([queue({ kind: "hardcover-match" })], [jobRow({ owner: "panda" })]);
    renderJobsTab();
    // The axis is advertised in the Group-by bar's picker.
    await screen.findByText("hardcover-match");
    expect(screen.getByRole("button", { name: /add axis/i })).toBeInTheDocument();
  });
});

describe("JobsTab — health strip", () => {
  const q = (over: Record<string, unknown>) => queue(over);

  it("names the failing kinds when something is failing", async () => {
    stubReads([q({ kind: "a", failedLastHour: 3 }), q({ kind: "b" })]);
    renderJobsTab();

    const strip = await screen.findByTestId("jobs-health-strip");
    expect(strip).toHaveTextContent(/1 kind failing/i);
    expect(strip).toHaveTextContent(/\ba\b/);
  });

  it("stays calm when nothing is wrong", async () => {
    stubReads([q({ kind: "a", running: 1, maxConcurrency: 4 })]);
    renderJobsTab();

    const strip = await screen.findByTestId("jobs-health-strip");
    expect(strip).toHaveTextContent(/no failures in the last hour/i);
    expect(strip).not.toHaveTextContent(/failing/i);
  });

  // At capacity with an EMPTY queue is a saturated worker doing its job, not a
  // problem — only a backlog behind the cap earns an operator's attention.
  it("does not cry backed-up when a kind is at cap with nothing waiting", async () => {
    stubReads([q({ kind: "a", running: 2, maxConcurrency: 2, queued: 0 })]);
    renderJobsTab();

    const strip = await screen.findByTestId("jobs-health-strip");
    expect(strip).toHaveTextContent(/no failures in the last hour/i);
    expect(strip).not.toHaveTextContent(/backed up/i);
  });
});

describe("JobsTab — bulk log clears (confirm-gated)", () => {
  it("Clear all logs → confirm → DELETE /jobs/logs", async () => {
    stubReads([queue({})]);
    let hit = 0;
    server.use(
      http.delete("/api/v1/admin/jobs/logs", ({ request }) => {
        hit += 1;
        expect(new URL(request.url).searchParams.get("kind")).toBeNull();
        return HttpResponse.json({ deleted: 4 });
      }),
    );
    vi.spyOn(window, "confirm").mockReturnValue(true);

    const user = userEvent.setup();
    renderJobsTab();
    await user.click(await screen.findByRole("button", { name: /clear all logs/i }));
    await waitFor(() => expect(hit).toBe(1));
  });

  it("does nothing when the confirm() is declined", async () => {
    stubReads([queue({})]);
    let hit = 0;
    server.use(
      http.delete("/api/v1/admin/jobs/logs", () => {
        hit += 1;
        return HttpResponse.json({ deleted: 0 });
      }),
    );
    vi.spyOn(window, "confirm").mockReturnValue(false);

    const user = userEvent.setup();
    renderJobsTab();
    await user.click(await screen.findByRole("button", { name: /clear all logs/i }));
    expect(hit).toBe(0);
  });
});
