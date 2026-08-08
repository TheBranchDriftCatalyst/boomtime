// GithubCharts.test.tsx (gaka-v1k P4) — the three GH-only chart widgets, each
// exercised across the additive invariant (bd memories github-stats):
//
//   1. Feature off (github_connect_enabled === false)  → renders NOTHING.
//   2. Feature on, not connected (github/stats 404)     → "Connect GitHub" CTA
//      linking to /app/settings?tab=profile.
//   3. Feature on, data present                         → the chart.
//
// Mirrors GithubStatTiles.test.tsx's MSW setup. All three share ONE cached
// query, so the assertions below also implicitly cover that they read the same
// payload.

import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import {
  GithubCommitsCard,
  GithubReposCard,
  GithubLanguagesCard,
} from "@/features/overview/GithubCharts";
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
    stars: 4200,
    publicRepos: 30,
    publicGists: 4,
    accountAgeDays: 3650,
  },
  contributionGrid: [
    { date: "2026-07-20", count: 4 },
    { date: "2026-07-27", count: 9 },
    { date: "2026-08-03", count: 6 },
  ],
  topRepos: [
    { name: "hyperdrive", stars: 3200, language: "Go", url: "https://github.com/octocat/hyperdrive" },
    { name: "synthwave-ui", stars: 900, language: "TypeScript", url: "https://github.com/octocat/synthwave-ui" },
  ],
  languages: [
    { name: "Go", bytes: 800000 },
    { name: "TypeScript", bytes: 200000 },
  ],
  fetchedAt: "2026-08-07T00:00:00Z",
};

function serveData() {
  server.use(
    http.get("/api/v1/users/current/github/stats", () =>
      HttpResponse.json(POPULATED),
    ),
  );
}
function serve404() {
  server.use(
    http.get("/api/v1/users/current/github/stats", () =>
      HttpResponse.json({ error: "not connected" }, { status: 404 }),
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
afterEach(() => authStore.clear());

const CARDS: Array<[string, () => ReactElement]> = [
  ["GithubCommitsCard", () => <GithubCommitsCard />],
  ["GithubReposCard", () => <GithubReposCard />],
  ["GithubLanguagesCard", () => <GithubLanguagesCard />],
];

describe.each(CARDS)("%s (gaka-v1k P4) — invariant states", (_name, Card) => {
  it("renders nothing when the feature is disabled server-side", async () => {
    enableFeature(false);
    const { container } = renderWithProviders(<Card />, { withRouter: true });
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the Connect GitHub CTA (linking to settings) when not connected", async () => {
    enableFeature(true);
    serve404();
    renderWithProviders(<Card />, { withRouter: true });
    await waitFor(() =>
      expect(
        screen.getByText(/connect github to see your commits/i),
      ).toBeInTheDocument(),
    );
    const link = screen.getByRole("link", { name: /connect github/i });
    expect(link).toHaveAttribute("href", "/app/settings?tab=profile");
  });

  it("shows the Connect CTA when connected but the payload is empty", async () => {
    enableFeature(true);
    // msw default handler serves an all-zero, empty payload → no data.
    renderWithProviders(<Card />, { withRouter: true });
    await waitFor(() =>
      expect(
        screen.getByText(/connect github to see your commits/i),
      ).toBeInTheDocument(),
    );
  });
});

describe("GithubCommitsCard — data state", () => {
  it("renders the commits headline from the cached payload", async () => {
    enableFeature(true);
    serveData();
    renderWithProviders(<GithubCommitsCard />, { withRouter: true });
    await waitFor(() => expect(screen.getByText("Commits")).toBeInTheDocument());
    // 1234 → compact "1.2K"
    expect(screen.getByText("1.2K")).toBeInTheDocument();
    expect(
      screen.queryByText(/connect github to see your commits/i),
    ).not.toBeInTheDocument();
  });
});

describe("GithubReposCard — data state", () => {
  it("lists top repositories by stars, linked out", async () => {
    enableFeature(true);
    serveData();
    renderWithProviders(<GithubReposCard />, { withRouter: true });
    await waitFor(() =>
      expect(screen.getByText("hyperdrive")).toBeInTheDocument(),
    );
    expect(screen.getByText("synthwave-ui")).toBeInTheDocument();
    const repoLink = screen.getByRole("link", { name: "hyperdrive" });
    expect(repoLink).toHaveAttribute(
      "href",
      "https://github.com/octocat/hyperdrive",
    );
  });
});

describe("GithubLanguagesCard — data state", () => {
  it("renders a language legend from the byte breakdown", async () => {
    enableFeature(true);
    serveData();
    renderWithProviders(<GithubLanguagesCard />, { withRouter: true });
    await waitFor(() => expect(screen.getByText("Go")).toBeInTheDocument());
    expect(screen.getByText("TypeScript")).toBeInTheDocument();
    // Go = 800k / 1M = 80.0%
    expect(screen.getByText("80.0%")).toBeInTheDocument();
  });
});
