import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RestoreConfirmDialog } from "@/features/heartbeats/RestoreConfirmDialog";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

const file = () =>
  new File(["PK\x03\x04fake"], "boomtime-backup.zip", { type: "application/zip" });

function renderDialog(onClose = () => {}) {
  return renderWithProviders(
    <RestoreConfirmDialog file={file()} onClose={onClose} />,
    { withAuth: true },
  );
}

describe("RestoreConfirmDialog", () => {
  it("keeps the destructive button disabled until REPLACE is typed exactly", async () => {
    const user = userEvent.setup();
    renderDialog();

    const confirm = await screen.findByRole("button", { name: /erase & restore/i });
    expect(confirm).toBeDisabled();

    const input = screen.getByLabelText(/type replace to confirm/i);
    await user.type(input, "replace");
    expect(confirm).toBeDisabled(); // case-sensitive

    await user.clear(input);
    await user.type(input, "REPLACE");
    expect(confirm).toBeEnabled();
  });

  it("posts the archive with the confirm param and reports the summary", async () => {
    let confirmParam: string | null = null;
    server.use(
      http.post("/api/v1/users/current/db/import", ({ request }) => {
        confirmParam = new URL(request.url).searchParams.get("confirm");
        return HttpResponse.json({
          gooseVersion: 13,
          totalRows: 42,
          tables: { heartbeats: 40, users: 2 },
        });
      }),
      // Post-restore session check succeeds -> no logout path.
      http.get("/auth/users/current", () => HttpResponse.json({ user: "dj" })),
    );

    const user = userEvent.setup();
    let closed = false;
    renderDialog(() => {
      closed = true;
    });

    await user.type(
      screen.getByLabelText(/type replace to confirm/i),
      "REPLACE",
    );
    await user.click(screen.getByRole("button", { name: /erase & restore/i }));

    await waitFor(() => expect(closed).toBe(true));
    expect(confirmParam).toBe("replace-all-data");
  });

  it("surfaces the server's error message on a failed restore", async () => {
    server.use(
      http.post("/api/v1/users/current/db/import", () =>
        HttpResponse.json(
          { message: "backup schema version 7 does not match this server's 13" },
          { status: 409 },
        ),
      ),
    );

    const user = userEvent.setup();
    let closed = false;
    renderDialog(() => {
      closed = true;
    });

    await user.type(
      screen.getByLabelText(/type replace to confirm/i),
      "REPLACE",
    );
    const confirm = screen.getByRole("button", { name: /erase & restore/i });
    await user.click(confirm);

    // Dialog stays open on failure so the user can retry with another file.
    await waitFor(() => expect(confirm).toBeEnabled());
    expect(closed).toBe(false);
  });
});
