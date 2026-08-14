// JobLogStream.test.tsx — the FINISHED-job path (gaka-hney): a done/failed job
// renders its DURABLE stored logs (GET .../logs), and the trash button deletes
// just the stored object (DELETE .../logs) then drops to the empty state.
//
// Non-tautological:
//   1. A finished job fetches + renders the persisted lines (mocked getJobLogs).
//   2. The delete icon confirms, calls deleteJobLogs, and clears the view.
//   3. A finished job with NO stored logs shows the empty state (no crash).
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

// The live path must NOT be used for a finished job — mock the socket to throw
// if it's ever mounted, proving the finished branch reads stored logs instead.
vi.mock("@/features/logs/useLogsSocket", () => ({
  useLogsSocket: () => {
    throw new Error("useLogsSocket must not run for a finished job");
  },
}));

import { JobLogStream } from "@/features/logs/JobLogStream";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

const storedEntries = [
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
    msg: "persisted line survives the ring",
    attrs: { job_id: "7" },
    source: "worker",
  },
];

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

describe("JobLogStream — finished job stored logs", () => {
  it("renders the persisted logs fetched from GET .../logs", async () => {
    server.use(
      http.get("/api/v1/admin/jobs/7/logs", () =>
        HttpResponse.json({ entries: storedEntries }),
      ),
    );

    renderWithProviders(<JobLogStream jobId={7} status="done" />);

    expect(await screen.findByText("jobs: started")).toBeInTheDocument();
    expect(
      screen.getByText("persisted line survives the ring"),
    ).toBeInTheDocument();
  });

  it("delete icon confirms, calls DELETE .../logs, and clears the view", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    let deleteHits = 0;
    server.use(
      http.get("/api/v1/admin/jobs/7/logs", () =>
        HttpResponse.json({ entries: storedEntries }),
      ),
      http.delete("/api/v1/admin/jobs/7/logs", () => {
        deleteHits += 1;
        return HttpResponse.json({ deleted: true });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<JobLogStream jobId={7} status="failed" />);

    await screen.findByText("jobs: started");
    await user.click(
      screen.getByRole("button", { name: /delete stored logs/i }),
    );

    expect(confirmSpy).toHaveBeenCalledOnce();
    await waitFor(() => expect(deleteHits).toBe(1));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Stored logs deleted"),
    );
    // View cleared → the persisted line is gone and the empty state shows.
    await waitFor(() =>
      expect(screen.queryByText("jobs: started")).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/no stored logs for this job/i)).toBeInTheDocument();
  });

  it("shows the empty state when the job has no stored logs (404 → [])", async () => {
    server.use(
      http.get("/api/v1/admin/jobs/9/logs", () =>
        HttpResponse.json({ error: "no stored logs for this job" }, { status: 404 }),
      ),
    );

    renderWithProviders(<JobLogStream jobId={9} status="cancelled" />);

    await waitFor(() =>
      expect(
        screen.getByText(/no stored logs for this job/i),
      ).toBeInTheDocument(),
    );
    // No delete button when there's nothing stored.
    expect(
      screen.queryByRole("button", { name: /delete stored logs/i }),
    ).not.toBeInTheDocument();
  });
});
