// GithubStatTiles.test.tsx (gaka-csx P3) — the three states of the GH-only
// Overview surface, per invariant case (B):
//
//   1. Feature off (github_connect_enabled === false)  → renders NOTHING.
//   2. Feature on, not connected (github/stats 404)     → "Connect GitHub" CTA
//      linking to /app/settings?tab=profile.
//   3. Feature on, data present                         → GH-branded tiles.
//
// Plus a unit check of the streak derivation, which the "days" tile depends on.

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { GithubStatTiles } from "@/features/overview/GithubStatTiles";
import { currentGithubStreak } from "@/features/overview/githubStreak";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

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

const POPULATED = {
  login: "octocat",
  totals: {
    commits: 1234,
    pullRequests: 40,
    pullRequestReviews: 87,
    issues: 12,
    repositories: 9,
    restrictedPrivate: 3,
    totalContributions: 2500,
    followers: 100,
    following: 20,
    stars: 500,
    publicRepos: 30,
    publicGists: 4,
    accountAgeDays: 3650,
  },
  contributionGrid: [
    { date: "2026-08-05", count: 4 },
    { date: "2026-08-06", count: 2 },
    { date: "2026-08-07", count: 1 },
  ],
  topRepos: [],
  languages: [],
  fetchedAt: "2026-08-07T00:00:00Z",
};

beforeEach(() => {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
});
afterEach(() => authStore.clear());

describe("GithubStatTiles (gaka-csx P3)", () => {
  it("renders nothing when the feature is disabled server-side", async () => {
    enableFeature(false);
    const { container } = renderWithProviders(<GithubStatTiles />, {
      withRouter: true,
    });
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the Connect GitHub CTA (linking to settings) when not connected", async () => {
    enableFeature(true);
    server.use(
      http.get("/api/v1/users/current/github/stats", () =>
        HttpResponse.json({ error: "not connected" }, { status: 404 }),
      ),
    );
    renderWithProviders(<GithubStatTiles />, { withRouter: true });

    await waitFor(() =>
      expect(
        screen.getByText(/connect github to see your commits/i),
      ).toBeInTheDocument(),
    );
    const link = screen.getByRole("link", { name: /connect github/i });
    expect(link).toHaveAttribute("href", "/app/settings?tab=profile");
    // No tiles in the CTA state.
    expect(screen.queryByText(/total commits/i)).not.toBeInTheDocument();
  });

  it("shows the Connect CTA when connected but the payload is empty", async () => {
    enableFeature(true);
    // msw default handler serves an all-zero, empty-grid payload → no data.
    renderWithProviders(<GithubStatTiles />, { withRouter: true });
    await waitFor(() =>
      expect(
        screen.getByText(/connect github to see your commits/i),
      ).toBeInTheDocument(),
    );
  });

  it("renders GH-branded tiles when data is present", async () => {
    enableFeature(true);
    server.use(
      http.get("/api/v1/users/current/github/stats", () =>
        HttpResponse.json(POPULATED),
      ),
    );
    renderWithProviders(<GithubStatTiles />, { withRouter: true });

    await waitFor(() =>
      expect(screen.getByText("Total commits")).toBeInTheDocument(),
    );
    expect(screen.getByText("1,234")).toBeInTheDocument(); // commits, localized
    expect(screen.getByText("PR reviews")).toBeInTheDocument();
    expect(screen.getByText("87")).toBeInTheDocument(); // pr reviews
    expect(screen.getByText("Current GitHub streak")).toBeInTheDocument();
    expect(screen.getByText("2,500")).toBeInTheDocument(); // total contributions
    // Header attributes the strip to the login.
    expect(screen.getByText("@octocat")).toBeInTheDocument();
    // No CTA in the data state.
    expect(
      screen.queryByText(/connect github to see your commits/i),
    ).not.toBeInTheDocument();
  });
});

describe("currentGithubStreak", () => {
  it("counts consecutive trailing contribution days", () => {
    expect(
      currentGithubStreak([
        { date: "2026-08-05", count: 4 },
        { date: "2026-08-06", count: 2 },
        { date: "2026-08-07", count: 1 },
      ]),
    ).toBe(3);
  });

  it("gives today a grace day (empty last day doesn't zero the streak)", () => {
    expect(
      currentGithubStreak([
        { date: "2026-08-05", count: 4 },
        { date: "2026-08-06", count: 2 },
        { date: "2026-08-07", count: 0 }, // in-progress today
      ]),
    ).toBe(2);
  });

  it("breaks the streak on an interior gap", () => {
    expect(
      currentGithubStreak([
        { date: "2026-08-04", count: 9 },
        { date: "2026-08-05", count: 0 }, // gap
        { date: "2026-08-06", count: 2 },
        { date: "2026-08-07", count: 1 },
      ]),
    ).toBe(2);
  });

  it("returns 0 for an empty grid", () => {
    expect(currentGithubStreak([])).toBe(0);
  });
});
