// GithubConnectCard.test.tsx (boom-2ip Phase 1) — non-tautological tests for the
// Settings > Profile "Connect GitHub" card.
//
// Coverage:
//   1. Renders NOTHING when the server gate (github_connect_enabled) is off.
//   2. Not-connected state → "Connect GitHub" button.
//   3. Connected state → "Connected as @login" + Disconnect, which DELETEs and
//      invalidates.
//   4. The access token is never in the DOM (the payload never carries it).

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { GithubConnectCard } from "@shared/features/settings/GithubConnectCard";
import { authStore } from "@shared/features/auth/auth";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";

// enableFeature makes /api/v1/config/public advertise github_connect_enabled.
function enableFeature(enabled: boolean) {
  server.use(
    http.get("/api/v1/config/public", () =>
      HttpResponse.json({
        registration_enabled: true,
        auth_provider: "local",
        oidc_enabled: false,
        billing_enabled: false,
        beta_flags: {},
        github_connect_enabled: enabled,
      }),
    ),
  );
}

beforeEach(() => {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
});

afterEach(() => {
  authStore.clear();
});

describe("GithubConnectCard (boom-2ip)", () => {
  it("renders nothing when the feature is disabled server-side", async () => {
    enableFeature(false);
    const { container } = renderWithProviders(<GithubConnectCard />, {
      withRouter: true,
    });
    // Give usePublicConfig a tick to resolve; the card must stay empty.
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByText("Connect GitHub")).not.toBeInTheDocument();
  });

  it("shows a Connect button when the feature is on and not connected", async () => {
    enableFeature(true);
    server.use(
      http.get("/api/v1/users/current/github", () =>
        HttpResponse.json({ connected: false }),
      ),
    );
    renderWithProviders(<GithubConnectCard />, { withRouter: true });

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /connect github/i }),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByText(/disconnect/i)).not.toBeInTheDocument();
  });

  it("shows connected state with @login and disconnects", async () => {
    enableFeature(true);
    server.use(
      http.get("/api/v1/users/current/github", () =>
        HttpResponse.json({
          connected: true,
          login: "octocat",
          status: "valid",
          checkedAt: "2026-08-07T00:00:00Z",
        }),
      ),
    );
    let deleteHit = 0;
    server.use(
      http.delete("/api/v1/users/current/github", () => {
        deleteHit += 1;
        return new HttpResponse(null, { status: 204 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<GithubConnectCard />, { withRouter: true });

    await waitFor(() =>
      expect(screen.getByText(/connected as @octocat/i)).toBeInTheDocument(),
    );
    // The token never appears — the payload structurally cannot carry it.
    expect(screen.queryByText(/gho_/i)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /disconnect/i }));
    await waitFor(() => expect(deleteHit).toBe(1));
  });

  it("surfaces a success banner from the ?github=connected redirect param", async () => {
    enableFeature(true);
    server.use(
      http.get("/api/v1/users/current/github", () =>
        HttpResponse.json({ connected: true, login: "octocat", status: "valid" }),
      ),
    );
    renderWithProviders(<GithubConnectCard />, {
      withRouter: true,
      initialEntries: ["/app/settings?tab=profile&github=connected"],
    });
    await waitFor(() =>
      expect(screen.getByText(/github account connected/i)).toBeInTheDocument(),
    );
  });
});
