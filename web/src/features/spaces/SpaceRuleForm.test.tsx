import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SpaceRuleForm } from "@/features/spaces/SpaceRuleForm";
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
    http.post("/api/v1/users/current/spaces/:id/rules", async ({ request }) => {
      body.current = await request.json();
      return HttpResponse.json({
        rule: { id: 1, axis: "project", matchValue: "x", matchType: "exact" },
      });
    }),
  );
  return body;
}

describe("SpaceRuleForm (P0/P1)", () => {
  it("submits the exact rule payload shape", async () => {
    const body = capturePost();
    const onDone = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<SpaceRuleForm spaceId={5} onDone={onDone} />);

    // Default axis is `project`, default mode is exact — value is an
    // autocomplete combobox of real axis values (with counts). Open it and
    // pick the "boomtime" option (from the mocked group endpoint).
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("button", { name: /boomtime/i }));
    await user.click(screen.getByRole("button", { name: /add rule/i }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(body.current).toEqual({
      axis: "project",
      matchValue: "boomtime",
      matchType: "exact",
    });
  });

  it("sends matchType=regex when Regex mode is selected", async () => {
    const body = capturePost();
    const user = userEvent.setup();
    renderWithProviders(<SpaceRuleForm spaceId={5} />);

    await user.click(screen.getByRole("button", { name: "Regex" }));
    await user.type(screen.getByPlaceholderText("^catalyst"), "^catalyst");
    await user.click(screen.getByRole("button", { name: /add rule/i }));

    await waitFor(() =>
      expect((body.current as { matchType?: string })?.matchType).toBe("regex"),
    );
    expect(body.current).toMatchObject({
      axis: "project",
      matchValue: "^catalyst",
      matchType: "regex",
    });
  });

  it("fires the live preview call as the value is typed", async () => {
    let previewParams: URLSearchParams | null = null;
    server.use(
      http.get("/api/v1/users/current/spaces/preview", ({ request }) => {
        previewParams = new URL(request.url).searchParams;
        return HttpResponse.json({
          values: [{ value: "catalyst-api", count: 12 }],
          truncated: false,
        });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<SpaceRuleForm spaceId={5} />);

    await user.click(screen.getByRole("button", { name: "Regex" }));
    await user.type(screen.getByPlaceholderText("^catalyst"), "^catalyst");

    // The debounced preview should fire and render the matched value + count.
    await waitFor(() => expect(previewParams).not.toBeNull());
    expect(previewParams!.get("axis")).toBe("project");
    expect(previewParams!.get("matchValue")).toBe("^catalyst");
    expect(previewParams!.get("matchType")).toBe("regex");

    await waitFor(() =>
      expect(screen.getByText("catalyst-api")).toBeInTheDocument(),
    );
    expect(screen.getByText(/Matches 1 value/)).toBeInTheDocument();
  });

  it("rejects an invalid regex with an error toast and no request", async () => {
    let posted = false;
    server.use(
      http.post("/api/v1/users/current/spaces/:id/rules", () => {
        posted = true;
        return HttpResponse.json({ id: 1 });
      }),
    );
    toastError.mockClear();
    const user = userEvent.setup();
    renderWithProviders(<SpaceRuleForm spaceId={5} />);

    await user.click(screen.getByRole("button", { name: "Regex" }));
    await user.type(screen.getByPlaceholderText("^catalyst"), "(unclosed");
    await user.click(screen.getByRole("button", { name: /add rule/i }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        expect.stringMatching(/valid regular expression/i),
      ),
    );
    expect(posted).toBe(false);
  });
});
