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

    // Exact mode: the pattern field is an autocomplete combobox of real axis
    // values (creatable). Open it, type a value not in the list, and commit it.
    await user.click(screen.getByRole("combobox"));
    await user.type(screen.getByPlaceholderText(/Search projects/i), "gaka");
    await user.click(screen.getByRole("button", { name: /Use "gaka"/ }));
    await user.type(screen.getByPlaceholderText("Meeting"), "boomtime");
    await user.click(screen.getByRole("button", { name: /add/i }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(body.current).toEqual({
      axis: "project",
      action: "rename",
      matchValue: "gaka",
      newValue: "boomtime",
      matchType: "exact",
    });
  });

  it("sends matchType=regex when the Regex mode is selected", async () => {
    const body = capturePost();
    const user = userEvent.setup();
    renderWithProviders(<RemappingForm presetAxis="category" submitLabel="Add" />);

    await user.click(screen.getByRole("button", { name: "Regex" }));
    await user.type(screen.getByPlaceholderText("^Meet"), "^Meet");
    await user.type(screen.getByPlaceholderText("Meeting"), "Meeting");
    await user.click(screen.getByRole("button", { name: /^add$/i }));

    await waitFor(() =>
      expect((body.current as { matchType?: string })?.matchType).toBe("regex"),
    );
    expect(body.current).toMatchObject({
      matchValue: "^Meet",
      newValue: "Meeting",
      matchType: "regex",
    });
  });

  it("Capture mode sends matchType=template with $N translated to \\N", async () => {
    const body = capturePost();
    const user = userEvent.setup();
    renderWithProviders(<RemappingForm presetAxis="project" submitLabel="Add" />);

    await user.click(screen.getByRole("button", { name: "Capture" }));
    await user.type(screen.getByPlaceholderText("^@(.*)$"), "^@(.*)$");
    await user.type(screen.getByPlaceholderText("$1"), "$1");
    await user.click(screen.getByRole("button", { name: /^add$/i }));

    await waitFor(() =>
      expect((body.current as { matchType?: string })?.matchType).toBe(
        "template",
      ),
    );
    expect(body.current).toMatchObject({
      matchValue: "^@(.*)$",
      newValue: "\\1", // `$1` -> backend `\1`
      matchType: "template",
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

    await user.click(screen.getByRole("button", { name: "Regex" }));
    await user.type(screen.getByPlaceholderText("^Meet"), "(unclosed");
    await user.type(screen.getByPlaceholderText("Meeting"), "Meeting");
    await user.click(screen.getByRole("button", { name: /^add$/i }));

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

describe("RemappingForm edit mode (gaka-cn1)", () => {
  it("pre-fills from the rule and upserts (create only) when only the target changes", async () => {
    const body = capturePost();
    let deleted = false;
    server.use(
      http.delete("/api/v1/users/current/curation/:id", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const onDone = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <RemappingForm
        editRuleId={7}
        presetAxis="project"
        presetValue="Meet"
        presetMatchType="exact"
        presetTarget="Meeting"
        submitLabel="Save"
        onDone={onDone}
      />,
    );

    // Target field is pre-filled with the current mapping.
    const targetInput = screen.getByDisplayValue("Meeting");
    // Change ONLY the target — pattern/matchType unchanged → upsert (no delete).
    await user.clear(targetInput);
    await user.type(targetInput, "Standup");
    await user.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(body.current).toEqual({
      axis: "project",
      action: "rename",
      matchValue: "Meet",
      newValue: "Standup",
      matchType: "exact",
    });
    expect(deleted).toBe(false);
  });

  it("deletes the old rule then creates a new one when the pattern changes", async () => {
    const body = capturePost();
    const deletedIds: string[] = [];
    server.use(
      http.delete("/api/v1/users/current/curation/:id", ({ params }) => {
        deletedIds.push(String(params.id));
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const onDone = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <RemappingForm
        editRuleId={7}
        presetAxis="category"
        presetValue="^Meet"
        presetMatchType="regex"
        presetTarget="Meeting"
        submitLabel="Save"
        onDone={onDone}
      />,
    );

    // Regex mode → pattern is a plain Input pre-filled with "^Meet".
    const patternInput = screen.getByDisplayValue("^Meet");
    await user.clear(patternInput);
    await user.type(patternInput, "^Sync");
    await user.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    // Identity (matchValue) changed → old rule deleted, new one created.
    expect(deletedIds).toEqual(["7"]);
    expect(body.current).toMatchObject({
      axis: "category",
      matchValue: "^Sync",
      newValue: "Meeting",
      matchType: "regex",
    });
  });
});
