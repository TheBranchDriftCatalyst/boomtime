import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RemappingRow } from "@/features/curation/RemappingRow";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { CurationRule } from "@/types/api";

// gaka-dfd: eyeball toggle on curation rows. The row renders an EyeOff icon
// when the rule is enabled ("click to pause") and an Eye icon when disabled
// ("click to resume"). Clicking POSTs {enabled: !current} to the toggle
// endpoint. The row's opacity dims when paused so the "not doing anything"
// signal is present at a glance.

function makeRule(overrides: Partial<CurationRule> = {}): CurationRule {
  return {
    id: 42,
    axis: "project",
    action: "rename",
    matchValue: "old-name",
    newValue: "new-name",
    matchType: "exact",
    enabled: true,
    applyAtIngest: false,
    createdAt: "2025-01-01T00:00:00Z",
    ...overrides,
  };
}

// AddToSpaceDropdown (used by the row) uses useNavigate — every test must
// mount inside a router. `withRouter: true` is enough.
const RENDER_OPTS = { withRouter: true };

describe("RemappingRow — toggle (gaka-dfd)", () => {
  it("renders EyeOff when enabled and Eye when disabled", () => {
    const noop = () => undefined;
    const { rerender } = renderWithProviders(
      <RemappingRow rule={makeRule({ enabled: true })} onRemove={noop} />,
      RENDER_OPTS,
    );
    // EyeOff button ('pause') should be present.
    expect(
      screen.getByRole("button", { name: /pause curation rule/i }),
    ).toBeInTheDocument();

    rerender(<RemappingRow rule={makeRule({ enabled: false })} onRemove={noop} />);
    expect(
      screen.getByRole("button", { name: /enable curation rule/i }),
    ).toBeInTheDocument();
  });

  it("clicking the toggle POSTs enabled: !current to /toggle", async () => {
    // Capture the outgoing request so we can assert its body — the FE must
    // send the EXACT desired state, not a flip, to defend against double-
    // click races landing on the wrong value.
    const captured: { body: unknown; path: string | null } = {
      body: undefined,
      path: null,
    };
    server.use(
      http.post(
        "/api/v1/users/current/curation/:id/toggle",
        async ({ request, params }) => {
          captured.path = params.id as string;
          captured.body = await request.json();
          return HttpResponse.json({ enabled: false });
        },
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(
      <RemappingRow rule={makeRule({ enabled: true })} onRemove={() => undefined} />,
      RENDER_OPTS,
    );

    await user.click(
      screen.getByRole("button", { name: /pause curation rule/i }),
    );

    await waitFor(() => expect(captured.path).toBe("42"));
    expect(captured.body).toEqual({ enabled: false });
  });

  it("dims the row (opacity-60) when the rule is disabled", () => {
    const { container } = renderWithProviders(
      <RemappingRow
        rule={makeRule({ enabled: false })}
        onRemove={() => undefined}
      />,
      RENDER_OPTS,
    );
    // The row is the outermost <div> inside the render — check its class list.
    const row = container.firstChild as HTMLElement;
    expect(row.className).toMatch(/opacity-60/);
  });

  it("does NOT dim the row when the rule is enabled", () => {
    const { container } = renderWithProviders(
      <RemappingRow
        rule={makeRule({ enabled: true })}
        onRemove={() => undefined}
      />,
      RENDER_OPTS,
    );
    const row = container.firstChild as HTMLElement;
    expect(row.className).not.toMatch(/opacity-60/);
  });

  it("treats missing `enabled` as true (pre-migration back-compat)", () => {
    // enabled deliberately omitted — the FE should treat this as "on" so
    // pre-migration installs render sane.
    const { enabled: _drop, ...withoutEnabled } = makeRule();
    void _drop;

    renderWithProviders(
      <RemappingRow
        rule={withoutEnabled as CurationRule}
        onRemove={() => undefined}
      />,
      RENDER_OPTS,
    );
    // Renders the "pause" affordance (implying it thinks the rule is enabled).
    expect(
      screen.getByRole("button", { name: /pause curation rule/i }),
    ).toBeInTheDocument();
  });
});

describe("RemappingRow — ingest badge (gaka)", () => {
  it("shows the ingest badge when applyAtIngest is true", () => {
    renderWithProviders(
      <RemappingRow
        rule={makeRule({ applyAtIngest: true })}
        onRemove={() => undefined}
      />,
      RENDER_OPTS,
    );
    expect(screen.getByText("ingest")).toBeInTheDocument();
  });

  it("omits the ingest badge for a plain query-time view rule", () => {
    renderWithProviders(
      <RemappingRow
        rule={makeRule({ applyAtIngest: false })}
        onRemove={() => undefined}
      />,
      RENDER_OPTS,
    );
    expect(screen.queryByText("ingest")).not.toBeInTheDocument();
  });

  it("treats missing applyAtIngest as false (no badge)", () => {
    const { applyAtIngest: _drop, ...withoutIngest } = makeRule();
    void _drop;
    renderWithProviders(
      <RemappingRow
        rule={withoutIngest as CurationRule}
        onRemove={() => undefined}
      />,
      RENDER_OPTS,
    );
    expect(screen.queryByText("ingest")).not.toBeInTheDocument();
  });
});

// Silence unused-import warning during red/green iteration.
void vi;
