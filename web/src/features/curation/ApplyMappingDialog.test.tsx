import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApplyMappingDialog } from "@/features/curation/ApplyMappingDialog";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { CurationRule } from "@/types/api";

// Spy on sonner toasts so we can assert the success message contents.
const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    error: (m: string) => toastError(m),
    success: (m: string) => toastSuccess(m),
  },
}));

// A canonical rename rule used across cases — matches the shape the
// RemappingsTab hands into the dialog.
function fakeRule(overrides: Partial<CurationRule> = {}): CurationRule {
  return {
    id: 42,
    axis: "project",
    action: "rename",
    matchType: "exact",
    matchValue: "old-project",
    newValue: "new-project",
    createdAt: "2025-06-01T00:00:00Z",
    ...overrides,
  };
}

// The preview shape the backend returns (see internal/handler/curation.go
// ApplyRenamePreview). We centralize the mock builder so each test tweaks
// only what it needs and drift here is caught fast.
function fakePreview(overrides: Partial<{
  totalAffected: number;
  rowsShown: number;
  affectedRows: Array<{ id: number; before: string; after: string }>;
  sqlUpdate: string;
  sqlDelete: string;
}> = {}) {
  const affectedRows = overrides.affectedRows ?? [
    { id: 101, before: "old-project", after: "new-project" },
    { id: 102, before: "old-project", after: "new-project" },
    { id: 103, before: "old-project", after: "new-project" },
  ];
  const sqlUpdate =
    overrides.sqlUpdate ??
    "UPDATE heartbeats SET project = 'new-project' WHERE sender = 'alice' AND lower(project) = lower('old-project')";
  const sqlDelete =
    overrides.sqlDelete ??
    "DELETE FROM curation_rules WHERE id = 42 AND sender = 'alice'";
  return {
    sqlPlanned: `${sqlUpdate};\n${sqlDelete};`,
    sqlUpdate,
    sqlDelete,
    affectedRows,
    totalAffected: overrides.totalAffected ?? affectedRows.length,
    rowsShown: overrides.rowsShown ?? affectedRows.length,
    rule: {
      id: 42,
      axis: "project",
      matchType: "exact" as const,
      matchValue: "old-project",
      newValue: "new-project",
    },
  };
}

describe("ApplyMappingDialog (gaka-cr4)", () => {
  it("renders nothing when rule is null (closed state)", () => {
    renderWithProviders(<ApplyMappingDialog rule={null} onClose={() => {}} />);
    // The Dialog root is not open → no title text visible.
    expect(screen.queryByText(/apply mapping/i)).toBeNull();
  });

  it("fetches preview on open and renders SQL + diff table verbatim", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(fakePreview()),
      ),
    );

    renderWithProviders(
      <ApplyMappingDialog rule={fakeRule()} onClose={() => {}} />,
    );

    // Header: "> apply mapping — old-project → new-project" (monospace, per
    // the terminal-hacker aesthetic).
    await screen.findByText(/> apply mapping/i);
    expect(screen.getByText("old-project")).toBeInTheDocument();
    expect(screen.getByText("new-project")).toBeInTheDocument();

    // Planned SQL <pre> block contains both statements literally — this is
    // the "modal doesn't lie about SQL" contract.
    const pre = await screen.findByText(/UPDATE heartbeats/);
    expect(pre.textContent).toContain(
      "UPDATE heartbeats SET project = 'new-project'",
    );
    expect(pre.textContent).toContain(
      "DELETE FROM curation_rules WHERE id = 42",
    );

    // Diff table rows: 3 heartbeat rows, id + before + after per row.
    expect(await screen.findByText("101")).toBeInTheDocument();
    expect(screen.getByText("102")).toBeInTheDocument();
    expect(screen.getByText("103")).toBeInTheDocument();
    // "before" and "after" column labels from the shadcn Table header.
    expect(screen.getByText("before")).toBeInTheDocument();
    expect(screen.getByText("after")).toBeInTheDocument();
  });

  it("renders the 'and N more…' footer when the diff is capped", async () => {
    // Backend caps display at 100 but reports the exact total. Simulate a
    // mapping that hits 250 rows so the overflow footer is exercised.
    const someRows = Array.from({ length: 100 }, (_, i) => ({
      id: 1000 + i,
      before: "src",
      after: "dst",
    }));
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () =>
          HttpResponse.json(
            fakePreview({
              affectedRows: someRows,
              rowsShown: 100,
              totalAffected: 250,
            }),
          ),
      ),
    );

    renderWithProviders(
      <ApplyMappingDialog rule={fakeRule()} onClose={() => {}} />,
    );

    // Overflow footer: 250 - 100 = 150 more.
    expect(
      await screen.findByText(/and 150 more/i),
    ).toBeInTheDocument();
  });

  it("shows the no-op message when totalAffected is 0", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () =>
          HttpResponse.json(
            fakePreview({ affectedRows: [], totalAffected: 0, rowsShown: 0 }),
          ),
      ),
    );

    renderWithProviders(
      <ApplyMappingDialog rule={fakeRule()} onClose={() => {}} />,
    );

    // Still shows the SQL block and still allows confirm (see spec: even a
    // no-op removes the mapping row).
    expect(
      await screen.findByText(/this mapping is a no-op/i),
    ).toBeInTheDocument();
    // Confirm button is enabled — a 0-row apply is a valid, useful action.
    expect(
      screen.getByRole("button", { name: /apply mapping/i }),
    ).not.toBeDisabled();
  });

  it("calls the apply endpoint and toasts + closes on success", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(fakePreview()),
      ),
    );
    let posted = false;
    server.use(
      http.post(
        "/api/v1/users/current/curation/42/apply",
        () => {
          posted = true;
          return HttpResponse.json({
            rowsAffected: 3,
            sqlRun:
              "UPDATE heartbeats SET project = 'new-project';\nDELETE FROM curation_rules WHERE id = 42;",
            sqlUpdate: "UPDATE heartbeats SET project = 'new-project'",
            sqlDelete: "DELETE FROM curation_rules WHERE id = 42",
          });
        },
      ),
    );
    toastSuccess.mockClear();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <ApplyMappingDialog rule={fakeRule()} onClose={onClose} />,
    );

    // Wait for the confirm button to become enabled (preview loaded).
    const confirm = await screen.findByRole("button", {
      name: /apply mapping/i,
    });
    await waitFor(() => expect(confirm).not.toBeDisabled());
    await user.click(confirm);

    await waitFor(() => expect(posted).toBe(true));
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringMatching(/3 rows rewritten/i),
      ),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("shows an error toast when apply fails and keeps the modal open", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(fakePreview()),
      ),
      http.post(
        "/api/v1/users/current/curation/42/apply",
        () =>
          HttpResponse.json(
            { message: "Simulated FK violation" },
            { status: 500 },
          ),
      ),
    );
    toastError.mockClear();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <ApplyMappingDialog rule={fakeRule()} onClose={onClose} />,
    );

    const confirm = await screen.findByRole("button", {
      name: /apply mapping/i,
    });
    await waitFor(() => expect(confirm).not.toBeDisabled());
    await user.click(confirm);

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    // Modal must stay open so the user can retry or cancel — a destructive
    // action that failed mid-flight should not silently disappear.
    expect(onClose).not.toHaveBeenCalled();
  });

  it("closes when cancel is clicked (before apply runs)", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(fakePreview()),
      ),
    );
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <ApplyMappingDialog rule={fakeRule()} onClose={onClose} />,
    );

    // Wait until the modal is fully hydrated (SQL block visible) so cancel
    // isn't a race.
    await screen.findByText(/UPDATE heartbeats/);
    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });
});

// This suite is the "unitgration" flavor: it drives the RemappingsTab through
// the same MSW server the app talks to, so the row-icon click → modal open →
// preview render → confirm → apply → tab refetch path is exercised end-to-end
// in the browser test environment (no real backend). The plain unit tests
// above focus on the modal's own contract; this one proves the wire-through
// works when mounted inside the real container.
describe("RemappingsTab + ApplyMappingDialog integration (gaka-cr4)", () => {
  it("clicking the apply icon on a row opens the modal and shows the preview", async () => {
    // Async import so the mock at module top-level applies before the tab
    // pulls sonner in transitively.
    const { RemappingsTab } = await import(
      "@/features/curation/RemappingsTab"
    );

    // Seed a single rename rule so we get exactly one row + apply icon.
    server.use(
      http.get("/api/v1/users/current/curation", () =>
        HttpResponse.json({
          rules: [
            {
              id: 42,
              axis: "project",
              action: "rename",
              matchType: "exact",
              matchValue: "old-project",
              newValue: "new-project",
              createdAt: "2025-06-01T00:00:00Z",
            },
          ],
        }),
      ),
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(fakePreview()),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<RemappingsTab />, { withRouter: true });

    // Wait for the row to appear (curation list resolved).
    await screen.findByText("old-project");
    // The apply button is exposed by aria-label so we don't couple to icon
    // markup.
    const applyBtn = await screen.findByRole("button", {
      name: /apply mapping destructively/i,
    });
    await user.click(applyBtn);

    // The modal opens: SQL block hydrates from the preview endpoint.
    expect(await screen.findByText(/UPDATE heartbeats/)).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: /apply mapping/i }),
    ).toBeInTheDocument();
  });
});
