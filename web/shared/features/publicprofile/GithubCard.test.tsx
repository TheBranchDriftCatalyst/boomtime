// GithubCard.test.tsx (gaka-2ud P5) — the PUBLIC-profile GitHub tile, exercised
// across the PUBLIC additive invariant (critical difference from the in-app
// widgets: there is NEVER a CTA and NEVER an error on the public page):
//
//   1. Feature off (github_connect_enabled === false)  → renders NOTHING.
//   2. Feature on, not public / no cache (public mirror 404) → renders NOTHING.
//   3. Feature on, connected-but-empty payload          → renders NOTHING.
//   4. Feature on, data present                         → the charts.
//
// Data comes from the UNAUTH public mirror
// (GET /api/public/profile/:slug/github/stats), mocked here via MSW. No auth
// token is set — the public page is viewed by anonymous visitors.

import { afterEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { GithubCard } from "@shared/features/publicprofile/GithubCard";
import { renderWithProviders } from "@shared/test/renderWithProviders";
import { server } from "@shared/test/msw/server";
import { http, HttpResponse } from "@shared/test/msw/handlers";

const SLUG = "panda";

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

const EMPTY = {
  login: "octocat",
  totals: {
    commits: 0,
    pullRequests: 0,
    pullRequestReviews: 0,
    issues: 0,
    repositories: 0,
    restrictedPrivate: 0,
    totalContributions: 0,
    followers: 0,
    following: 0,
    stars: 0,
    publicRepos: 0,
    publicGists: 0,
    accountAgeDays: 0,
  },
  contributionGrid: [],
  topRepos: [],
  languages: [],
  fetchedAt: "2026-08-07T00:00:00Z",
};

function servePublic(status: number, body: unknown) {
  server.use(
    http.get("/api/public/profile/:slug/github/stats", () =>
      HttpResponse.json(body as object, { status }),
    ),
  );
}

afterEach(() => server.resetHandlers());

describe("GithubCard (gaka-2ud P5) — public hide-on-empty invariant", () => {
  it("renders NOTHING when the feature is disabled server-side", async () => {
    enableFeature(false);
    // Even if the endpoint would return data, the disabled feature short-circuits.
    servePublic(200, POPULATED);
    const { container } = renderWithProviders(<GithubCard slug={SLUG} />);
    await new Promise((r) => setTimeout(r, 20));
    expect(container).toBeEmptyDOMElement();
  });

  it("renders NOTHING (no CTA, no error) when the profile has no public GitHub (404)", async () => {
    enableFeature(true);
    servePublic(404, { error: "not public" });
    const { container } = renderWithProviders(<GithubCard slug={SLUG} />);
    await waitFor(() => expect(container).toBeEmptyDOMElement());
    // Explicitly assert the in-app CTA copy never appears on the public page.
    expect(
      screen.queryByText(/connect github/i),
    ).not.toBeInTheDocument();
  });

  it("renders NOTHING when connected but the payload is empty", async () => {
    enableFeature(true);
    servePublic(200, EMPTY);
    const { container } = renderWithProviders(<GithubCard slug={SLUG} />);
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it("renders the shared GitHub charts when the public mirror has data", async () => {
    enableFeature(true);
    servePublic(200, POPULATED);
    renderWithProviders(<GithubCard slug={SLUG} />);

    // Shared P3 tiles body.
    await waitFor(() =>
      expect(screen.getByText("Total commits")).toBeInTheDocument(),
    );
    expect(screen.getByText("PR reviews")).toBeInTheDocument();
    // Shared P4 commits headline.
    expect(screen.getByText("Commits")).toBeInTheDocument();
    // Shared P4 top-repos body — top repo links out.
    expect(screen.getByRole("link", { name: "hyperdrive" })).toHaveAttribute(
      "href",
      "https://github.com/octocat/hyperdrive",
    );
    // Shared P4 languages body — legend share (Go = 800k / 1M = 80.0%).
    expect(screen.getByText("80.0%")).toBeInTheDocument();
    // Attribution to the resolved login.
    expect(screen.getByText(/@octocat/)).toBeInTheDocument();
    // No CTA anywhere.
    expect(screen.queryByText(/connect github/i)).not.toBeInTheDocument();
  });
});
