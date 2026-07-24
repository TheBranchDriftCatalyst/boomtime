import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DestructiveActionDialog } from "@/features/curation/DestructiveActionDialog";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { CurationRule } from "@/types/api";

// Spy on sonner toasts so we can assert per-variant success + error copy.
const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    error: (m: string) => toastError(m),
    success: (m: string) => toastSuccess(m),
  },
}));

// Canonical rules — rename + hide — used across cases. Kept as factories so
// per-test overrides are trivial.
function renameRule(overrides: Partial<CurationRule> = {}): CurationRule {
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
function hideRule(overrides: Partial<CurationRule> = {}): CurationRule {
  return {
    id: 99,
    axis: "project",
    action: "hide",
    matchType: "exact",
    matchValue: "secret",
    newValue: null,
    createdAt: "2025-06-01T00:00:00Z",
    ...overrides,
  };
}

// Preview payload builders — one per variant. Shapes mirror the backend
// response from internal/handler/curation.go (ApplyRenamePreview dispatch).
function applyPreview(overrides: Partial<{
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
    action: "rename" as const,
    sqlPlanned: `${sqlUpdate};\n${sqlDelete};`,
    sqlUpdate,
    sqlDelete,
    affectedRows,
    totalAffected: overrides.totalAffected ?? affectedRows.length,
    rowsShown: overrides.rowsShown ?? affectedRows.length,
    rule: {
      id: 42,
      axis: "project",
      action: "rename" as const,
      matchType: "exact" as const,
      matchValue: "old-project",
      newValue: "new-project",
    },
  };
}
function purgePreview(overrides: Partial<{
  totalAffected: number;
  rowsShown: number;
  affectedRows: Array<{ id: number; deleted: Record<string, string> }>;
  sqlDeleteRows: string;
  sqlDeleteRule: string;
}> = {}) {
  const affectedRows = overrides.affectedRows ?? [
    { id: 201, deleted: { project: "secret" } },
    { id: 202, deleted: { project: "secret" } },
    { id: 203, deleted: { project: "secret" } },
  ];
  const sqlDeleteRows =
    overrides.sqlDeleteRows ??
    "DELETE FROM heartbeats WHERE sender = 'alice' AND lower(project) = lower('secret')";
  const sqlDeleteRule =
    overrides.sqlDeleteRule ??
    "DELETE FROM curation_rules WHERE id = 99 AND sender = 'alice'";
  return {
    action: "hide" as const,
    sqlPlanned: `${sqlDeleteRows};\n${sqlDeleteRule};`,
    sqlDeleteRows,
    sqlDeleteRule,
    affectedRows,
    totalAffected: overrides.totalAffected ?? affectedRows.length,
    rowsShown: overrides.rowsShown ?? affectedRows.length,
    rule: {
      id: 99,
      axis: "project",
      action: "hide" as const,
      matchType: "exact" as const,
      matchValue: "secret",
      newValue: null,
    },
  };
}

describe("DestructiveActionDialog — closed state", () => {
  it("renders nothing when rule is null", () => {
    renderWithProviders(
      <DestructiveActionDialog
        rule={null}
        onClose={() => {}}
        variant="apply"
      />,
    );
    expect(screen.queryByText(/> /i)).toBeNull();
  });
});

describe("DestructiveActionDialog — variant='apply' (rename)", () => {
  it("fetches preview, renders SQL verbatim, and shows before → after diff", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(applyPreview()),
      ),
    );

    renderWithProviders(
      <DestructiveActionDialog
        rule={renameRule()}
        onClose={() => {}}
        variant="apply"
      />,
    );

    // Header uses the apply verb + old → new target.
    await screen.findByText(/> apply rename/i);
    expect(screen.getByText("old-project")).toBeInTheDocument();
    expect(screen.getByText("new-project")).toBeInTheDocument();

    // SQL block contains both statements literally.
    const pre = await screen.findByText(/UPDATE heartbeats/);
    expect(pre.textContent).toContain(
      "UPDATE heartbeats SET project = 'new-project'",
    );
    expect(pre.textContent).toContain(
      "DELETE FROM curation_rules WHERE id = 42",
    );

    // Diff table: 3 rows with before + after columns.
    expect(await screen.findByText("101")).toBeInTheDocument();
    expect(screen.getByText("before")).toBeInTheDocument();
    expect(screen.getByText("after")).toBeInTheDocument();
  });

  it("confirm button enables WITHOUT a typing gate (apply is not gated)", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(applyPreview()),
      ),
    );
    renderWithProviders(
      <DestructiveActionDialog
        rule={renameRule()}
        onClose={() => {}}
        variant="apply"
      />,
    );

    // No typing input is rendered for the apply variant — the "type rule id"
    // label from the purge gate must not appear.
    expect(screen.queryByLabelText(/type rule id/i)).toBeNull();
    const confirm = await screen.findByRole("button", {
      name: /apply mapping/i,
    });
    await waitFor(() => expect(confirm).not.toBeDisabled());
  });

  it("calls the apply endpoint on confirm, toasts, and closes", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(applyPreview()),
      ),
    );
    let posted = false;
    server.use(
      http.post("/api/v1/users/current/curation/42/apply", () => {
        posted = true;
        return HttpResponse.json({
          rowsAffected: 3,
          sqlRun: "UPDATE ...",
          sqlUpdate: "UPDATE ...",
          sqlDelete: "DELETE ...",
        });
      }),
    );
    toastSuccess.mockClear();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <DestructiveActionDialog
        rule={renameRule()}
        onClose={onClose}
        variant="apply"
      />,
    );

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
});

describe("DestructiveActionDialog — variant='purge' (hide)", () => {
  it("renders SQL verbatim with DELETE heartbeats + shows 'will be deleted' column", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () => HttpResponse.json(purgePreview()),
      ),
    );

    renderWithProviders(
      <DestructiveActionDialog
        rule={hideRule()}
        onClose={() => {}}
        variant="purge"
      />,
    );

    // Header uses the purge verb; hide rules show pattern only (no target).
    await screen.findByText(/> purge hidden/i);
    expect(screen.getByText("secret")).toBeInTheDocument();

    // SQL: both a DELETE against heartbeats AND against curation_rules.
    const pre = await screen.findByText(/DELETE FROM heartbeats/);
    expect(pre.textContent).toContain(
      "DELETE FROM heartbeats WHERE sender = 'alice'",
    );
    expect(pre.textContent).toContain(
      "DELETE FROM curation_rules WHERE id = 99",
    );

    // Diff table uses a single "will be deleted" column, not before/after.
    // The column header text is exactly "will be deleted" (the "N rows will
    // be deleted forever" subtitle also matches on regex, so we assert on
    // the header role instead).
    expect(
      await screen.findByRole("columnheader", { name: /^will be deleted$/i }),
    ).toBeInTheDocument();
    // Rows render as "col = value" (single-column shape). Three rows in the
    // mock preview each render the same "project = secret" cell — assert
    // 3 matches so a stray render (2 or 4) still fails the test.
    expect(screen.getAllByText("project = secret")).toHaveLength(3);
    expect(screen.getByText("201")).toBeInTheDocument();
  });

  it("BLOCKS confirm until the rule id is typed exactly", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () => HttpResponse.json(purgePreview()),
      ),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <DestructiveActionDialog
        rule={hideRule()}
        onClose={() => {}}
        variant="purge"
      />,
    );

    // Wait for the modal body to hydrate (preview loaded) before we
    // interrogate the typing gate — the confirm button is rendered
    // synchronously by DialogFooter but the input only appears once the
    // preview resolves and the {cfg.requiresTypingGate && …} sub-tree mounts.
    // Short regex intentionally — long ones can miss nested-span label text.
    const input = await screen.findByLabelText(/type rule id 99/i);
    const confirm = screen.getByRole("button", {
      name: /delete rows forever/i,
    });
    // Muscle-memory defense: no destructive Enter by accident.
    expect(confirm).toBeDisabled();

    // Wrong id typed → still disabled.
    await user.type(input, "88");
    expect(confirm).toBeDisabled();

    // Right id → enabled.
    await user.clear(input);
    await user.type(input, "99");
    await waitFor(() => expect(confirm).not.toBeDisabled());
  });

  it("calls the PURGE endpoint on confirm (not apply)", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () => HttpResponse.json(purgePreview()),
      ),
    );
    // Wire both endpoints — verify apply is NOT hit (variant guard) and
    // purge IS hit.
    let applyHit = false;
    let purgeHit = false;
    server.use(
      http.post("/api/v1/users/current/curation/99/apply", () => {
        applyHit = true;
        return HttpResponse.json({ rowsAffected: 0, sqlRun: "", sqlUpdate: "", sqlDelete: "" });
      }),
      http.post("/api/v1/users/current/curation/99/purge", () => {
        purgeHit = true;
        return HttpResponse.json({
          rowsAffected: 3,
          sqlRun: "DELETE ...",
          sqlDeleteRows: "DELETE FROM heartbeats ...",
          sqlDeleteRule: "DELETE FROM curation_rules ...",
        });
      }),
    );
    toastSuccess.mockClear();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <DestructiveActionDialog
        rule={hideRule()}
        onClose={onClose}
        variant="purge"
      />,
    );

    const input = await screen.findByLabelText(/type rule id 99/i);
    await user.type(input, "99");
    const confirm = screen.getByRole("button", {
      name: /delete rows forever/i,
    });
    await waitFor(() => expect(confirm).not.toBeDisabled());
    await user.click(confirm);

    await waitFor(() => expect(purgeHit).toBe(true));
    expect(applyHit).toBe(false); // variant dispatch is correct
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith(
        expect.stringMatching(/3 rows deleted forever/i),
      ),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("shows the no-op message and STILL blocks confirm until id typed", async () => {
    // Even a 0-row purge deletes the rule — but we still make the user prove
    // they mean it (typing gate applies regardless of totalAffected).
    server.use(
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () =>
          HttpResponse.json(
            purgePreview({ affectedRows: [], totalAffected: 0, rowsShown: 0 }),
          ),
      ),
    );
    const user = userEvent.setup();
    renderWithProviders(
      <DestructiveActionDialog
        rule={hideRule()}
        onClose={() => {}}
        variant="purge"
      />,
    );

    // Noop message renders.
    expect(
      await screen.findByText(/this hide rule matches 0 rows/i),
    ).toBeInTheDocument();

    // But confirm is STILL disabled until the gate clears.
    const confirm = screen.getByRole("button", {
      name: /delete rows forever/i,
    });
    expect(confirm).toBeDisabled();

    await user.type(screen.getByLabelText(/type rule id 99/i), "99");
    await waitFor(() => expect(confirm).not.toBeDisabled());
  });

  it("surfaces backend errors as an error toast and keeps the modal open", async () => {
    server.use(
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () => HttpResponse.json(purgePreview()),
      ),
      http.post(
        "/api/v1/users/current/curation/99/purge",
        () =>
          HttpResponse.json({ message: "constraint violation" }, { status: 500 }),
      ),
    );
    toastError.mockClear();
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <DestructiveActionDialog
        rule={hideRule()}
        onClose={onClose}
        variant="purge"
      />,
    );

    await user.type(await screen.findByLabelText(/type rule id 99/i), "99");
    const confirm = screen.getByRole("button", {
      name: /delete rows forever/i,
    });
    await waitFor(() => expect(confirm).not.toBeDisabled());
    await user.click(confirm);

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe("DestructiveActionDialog — cross-variant + overflow", () => {
  it("renders the 'and N more…' footer identically in both variants", async () => {
    const bigApply = applyPreview({
      affectedRows: Array.from({ length: 100 }, (_, i) => ({
        id: 1000 + i,
        before: "s",
        after: "d",
      })),
      rowsShown: 100,
      totalAffected: 250,
    });
    server.use(
      http.get(
        "/api/v1/users/current/curation/42/preview",
        () => HttpResponse.json(bigApply),
      ),
    );
    const { unmount } = renderWithProviders(
      <DestructiveActionDialog
        rule={renameRule()}
        onClose={() => {}}
        variant="apply"
      />,
    );
    expect(
      await screen.findByText(/and 150 more/i),
    ).toBeInTheDocument();
    unmount();

    const bigPurge = purgePreview({
      affectedRows: Array.from({ length: 100 }, (_, i) => ({
        id: 2000 + i,
        deleted: { project: "secret" },
      })),
      rowsShown: 100,
      totalAffected: 250,
    });
    server.use(
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () => HttpResponse.json(bigPurge),
      ),
    );
    renderWithProviders(
      <DestructiveActionDialog
        rule={hideRule()}
        onClose={() => {}}
        variant="purge"
      />,
    );
    expect(
      await screen.findByText(/and 150 more/i),
    ).toBeInTheDocument();
  });
});

// Unitgration: mount the real RemappingsTab and verify the row's icon
// dispatch (zap on rename, trash on hide) opens the correct variant of the
// shared modal.
describe("RemappingsTab + DestructiveActionDialog wiring", () => {
  it("clicking the Zap icon on a rename row opens variant='apply'", async () => {
    const { RemappingsTab } = await import(
      "@/features/curation/RemappingsTab"
    );
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
        () => HttpResponse.json(applyPreview()),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<RemappingsTab />, { withRouter: true });

    await screen.findByText("old-project");
    const applyBtn = await screen.findByRole("button", {
      name: /apply mapping destructively/i,
    });
    await user.click(applyBtn);

    // Apply variant → "apply rename" header + "Apply mapping" confirm.
    expect(await screen.findByText(/> apply rename/i)).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: /apply mapping/i }),
    ).toBeInTheDocument();
  });

  it("clicking the Trash icon on a hide row opens variant='purge' with the typing gate", async () => {
    const { RemappingsTab } = await import(
      "@/features/curation/RemappingsTab"
    );
    server.use(
      http.get("/api/v1/users/current/curation", () =>
        HttpResponse.json({
          rules: [
            {
              id: 99,
              axis: "project",
              action: "hide",
              matchType: "exact",
              matchValue: "secret",
              newValue: null,
              createdAt: "2025-06-01T00:00:00Z",
            },
          ],
        }),
      ),
      http.get(
        "/api/v1/users/current/curation/99/preview",
        () => HttpResponse.json(purgePreview()),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<RemappingsTab />, { withRouter: true });

    await screen.findByText("secret");
    const purgeBtn = await screen.findByRole("button", {
      name: /purge hidden rows destructively/i,
    });
    await user.click(purgeBtn);

    // Purge variant → "purge hidden" header + typing gate visible + confirm
    // disabled until id typed.
    expect(await screen.findByText(/> purge hidden/i)).toBeInTheDocument();
    expect(
      await screen.findByLabelText(/type rule id 99/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /delete rows forever/i }),
    ).toBeDisabled();
  });
});
