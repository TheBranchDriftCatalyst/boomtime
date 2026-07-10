import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RemappingForm } from "@/components/curation/RemappingForm";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

// Spy on sonner toasts.
const toastError = vi.fn();
const toastSuccess = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    error: (m: string) => toastError(m),
    success: (m: string) => toastSuccess(m),
  },
}));

function capturePost() {
  const body: { current: unknown } = { current: undefined };
  server.use(
    http.post("/api/v1/users/current/curation", async ({ request }) => {
      body.current = await request.json();
      return HttpResponse.json({ rule: { id: 1 } });
    }),
  );
  return body;
}

describe("RemappingForm (P0/P1)", () => {
  it("submits the exact rename rule shape", async () => {
    const body = capturePost();
    const onDone = vi.fn();
    const user = userEvent.setup();
    // presetAxis locks the axis so we don't need the dropdown.
    renderWithProviders(
      <RemappingForm presetAxis="project" onDone={onDone} submitLabel="Add" />,
    );

    await user.type(screen.getByPlaceholderText(/Meet - Weekly/), "gaka");
    await user.type(screen.getByPlaceholderText("Meeting"), "gakatime");
    await user.click(screen.getByRole("button", { name: /add/i }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(body.current).toEqual({
      axis: "project",
      action: "rename",
      matchValue: "gaka",
      newValue: "gakatime",
      matchType: "exact",
    });
  });

  it("sends matchType=regex when the regex toggle is on", async () => {
    const body = capturePost();
    const user = userEvent.setup();
    renderWithProviders(<RemappingForm presetAxis="category" submitLabel="Add" />);

    await user.click(screen.getByRole("checkbox"));
    await user.type(screen.getByPlaceholderText("^Meet"), "^Meet");
    await user.type(screen.getByPlaceholderText("Meeting"), "Meeting");
    await user.click(screen.getByRole("button", { name: /add/i }));

    await waitFor(() =>
      expect((body.current as { matchType?: string })?.matchType).toBe("regex"),
    );
    expect(body.current).toMatchObject({
      matchValue: "^Meet",
      newValue: "Meeting",
      matchType: "regex",
    });
  });

  it("rejects an invalid regex with an error toast and no request", async () => {
    let posted = false;
    server.use(
      http.post("/api/v1/users/current/curation", () => {
        posted = true;
        return HttpResponse.json({ rule: { id: 1 } });
      }),
    );
    toastError.mockClear();
    const user = userEvent.setup();
    renderWithProviders(<RemappingForm presetAxis="category" submitLabel="Add" />);

    await user.click(screen.getByRole("checkbox"));
    await user.type(screen.getByPlaceholderText("^Meet"), "(unclosed");
    await user.type(screen.getByPlaceholderText("Meeting"), "Meeting");
    await user.click(screen.getByRole("button", { name: /add/i }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        expect.stringMatching(/valid regular expression/i),
      ),
    );
    expect(posted).toBe(false);
  });

  it("locks the axis when presetAxis is given (no axis dropdown button)", () => {
    renderWithProviders(<RemappingForm presetAxis="editor" />);
    // The locked axis is shown as static text "Editor", not a dropdown trigger.
    expect(screen.getByText("Editor")).toBeInTheDocument();
  });
});
