import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AddToSpaceDropdown } from "@/features/curation/AddToSpaceDropdown";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { CurationRule } from "@/types/api";

// gaka-esv: the AddToSpaceDropdown is FE-only composition on top of the
// existing spaces endpoints. Tests focus on:
//   1. matchType translation (template → regex when quick-adding to a Space)
//   2. POST body shape hitting /spaces/:id/rules
//   3. empty state offers a "create your first space" affordance
// The dropdown component uses useNavigate (via react-router) so all tests
// mount with `withRouter: true`.

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    success: (m: string) => toastSuccess(m),
    error: (m: string) => toastError(m),
  },
}));

function makeRule(overrides: Partial<CurationRule> = {}): CurationRule {
  return {
    id: 42,
    axis: "project",
    action: "rename",
    matchValue: "boomtime",
    newValue: "boom-web",
    matchType: "exact",
    enabled: true,
    applyAtIngest: false,
    createdAt: "2025-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("AddToSpaceDropdown (gaka-esv)", () => {
  it("posts the rule to /spaces/:id/rules with the exact axis+matchValue", async () => {
    const captured: { body: unknown; spaceId: string | null } = {
      body: undefined,
      spaceId: null,
    };
    server.use(
      http.get("/api/v1/users/current/spaces", () =>
        HttpResponse.json({
          spaces: [{ id: 7, name: "Work", position: 0, ruleCount: 3 }],
        }),
      ),
      http.post(
        "/api/v1/users/current/spaces/:id/rules",
        async ({ request, params }) => {
          captured.spaceId = params.id as string;
          captured.body = await request.json();
          return HttpResponse.json({
            rule: { id: 1, axis: "project", matchValue: "boomtime", matchType: "exact" },
          });
        },
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<AddToSpaceDropdown rule={makeRule()} />, {
      withRouter: true,
    });

    await user.click(
      screen.getByRole("button", { name: /add boomtime to a space/i }),
    );
    // The filter input receives autofocus; the Work item is a menuitem.
    await user.click(await screen.findByRole("menuitem", { name: /Work/ }));

    await waitFor(() => expect(captured.spaceId).toBe("7"));
    expect(captured.body).toEqual({
      axis: "project",
      matchValue: "boomtime",
      matchType: "exact",
    });
  });

  it("degrades template matchType to regex when adding to a space", async () => {
    // Template is a rename-only transform; space_rules only accept exact|regex.
    // The dropdown should quietly translate template→regex so the pattern
    // still matches the same rows in the Space (the transform is dropped).
    const captured: { body: unknown } = { body: undefined };
    server.use(
      http.get("/api/v1/users/current/spaces", () =>
        HttpResponse.json({
          spaces: [{ id: 3, name: "Personal", position: 0, ruleCount: 0 }],
        }),
      ),
      http.post(
        "/api/v1/users/current/spaces/:id/rules",
        async ({ request }) => {
          captured.body = await request.json();
          return HttpResponse.json({
            rule: { id: 1, axis: "project", matchValue: "^svc-", matchType: "regex" },
          });
        },
      ),
    );

    const user = userEvent.setup();
    const templateRule = makeRule({
      matchType: "template",
      matchValue: "^svc-(.*)$",
    });
    renderWithProviders(<AddToSpaceDropdown rule={templateRule} />, {
      withRouter: true,
    });

    await user.click(
      screen.getByRole("button", { name: /add .* to a space/i }),
    );
    await user.click(await screen.findByRole("menuitem", { name: /Personal/ }));

    await waitFor(() =>
      expect((captured.body as { matchType: string } | undefined)?.matchType).toBe(
        "regex",
      ),
    );
    expect(captured.body).toEqual({
      axis: "project",
      matchValue: "^svc-(.*)$",
      matchType: "regex",
    });
  });

  it("shows a create-your-first-space affordance when the user has no spaces", async () => {
    server.use(
      http.get("/api/v1/users/current/spaces", () =>
        HttpResponse.json({ spaces: [] }),
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<AddToSpaceDropdown rule={makeRule()} />, {
      withRouter: true,
    });
    await user.click(
      screen.getByRole("button", { name: /add boomtime to a space/i }),
    );

    expect(
      await screen.findByText(/You don't have any spaces yet/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: /Create your first space/i }),
    ).toBeInTheDocument();
  });
});
