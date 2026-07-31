// EditableProfilePage.test.tsx (gaka-ie3) — verify that the /p/:slug route
// dispatches correctly between:
//
//   1. Anonymous visitor (no auth cookie): read-only PublicDashboard
//      renders, NO edit chrome (no mode toggle, no save panel, no drag
//      handles).
//   2. Logged-in visitor on a foreign profile: same behavior as anonymous
//      — the non-owner MUST NOT see edit chrome (verifies the ownership
//      check actually gates edit chrome).
//   3. Logged-in visitor on their OWN profile: edit chrome is available
//      (mode toggle appears; flipping to Edit renders Save/Discard
//      chrome).
//
// These tests catch the specific regression class where a bad ownership
// check leaks edit UI to visitors — which would be worse than useless
// (drag handles that PUT would 403 anyway, but the visual leak alone is
// a bad UX bug).
import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { Route, Routes } from "react-router";
import { EditableProfilePage } from "@/features/publicprofile/EditableProfilePage";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import type { PublicDashboardPayload } from "@/types/stats";

// Silence the sonner toast bridge so save/discard side effects don't spew
// into stderr during tests.
vi.mock("sonner", () => {
  const toast = Object.assign((_: string) => {}, {
    error: (_: string) => {},
    success: (_: string) => {},
  });
  return { toast };
});

// Minimal payload — the widget renderers each guard on empty inputs, so
// most tiles render a "no data" state that still lets us assert the
// public shell (hero + username) is present.
function payload(username: string): PublicDashboardPayload {
  return {
    username,
    startDate: "2026-06-01T00:00:00Z",
    endDate: "2026-07-30T00:00:00Z",
    totalSeconds: 3600,
    dailyAvg: 60,
    dailyTotal: [3600],
    projects: [],
    languages: [],
    editors: [],
    platforms: [],
    categories: [],
    punchcard: { cells: [], maxSeconds: 0, totalSeconds: 3600 },
  };
}

// Base handlers for a public dashboard fetch. Tests override per-scenario
// for auth (currentUser + publicProfile) but the payload endpoint stays
// stable — it's slug-keyed and public.
function baseHandlers(over: {
  slug: string;
  ownerSlug?: string | null; // what the caller's own /profile returns (null = not logged in)
  ownerUsername?: string;
}) {
  const { slug, ownerSlug, ownerUsername } = over;
  const list = [
    http.get(`/api/public/profile/${slug}`, () =>
      HttpResponse.json(payload(slug)),
    ),
    // Awards for the profile — return empty so the hero-identity renderer
    // takes its "NEW OPERATOR" path (no network waste on avatars/labels).
    http.get(`/api/public/profile/${slug}/awards`, () => HttpResponse.json([])),
    http.get(`/api/public/profile/${slug}/awards/streaks`, () =>
      HttpResponse.json({}),
    ),
    // Existing HeroIdentity renderer ALWAYS calls the OWN streak endpoint
    // (retry:false in the hook so a 401 is silent in prod). Provide stub
    // handlers so MSW's onUnhandledRequest:error mode doesn't blow up the
    // test even on anonymous-visitor renders.
    http.get("/api/v1/users/current/awards/streaks", () =>
      HttpResponse.json({}),
    ),
    http.get("/api/v1/users/current/awards", () => HttpResponse.json([])),
    // Labels catalog: LabelChip renderer reads this. Public + no-auth.
    http.get("/api/v1/labels/catalog", () =>
      HttpResponse.json({ labels: [], systemPrompt: "" }),
    ),
  ];
  if (ownerUsername) {
    // /auth/users/current – shape from internal/handler/auth.go CurrentUser.
    list.push(
      http.get("/auth/users/current", () =>
        HttpResponse.json({
          data: {
            full_name: ownerUsername,
            email: `${ownerUsername}@x.dev`,
            photo: "",
            is_admin: false,
          },
        }),
      ),
    );
  }
  if (ownerSlug !== undefined) {
    list.push(
      http.get("/api/v1/users/current/profile", () =>
        HttpResponse.json({ enabled: ownerSlug != null, slug: ownerSlug }),
      ),
    );
  }
  return list;
}

function login(username: string) {
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: username,
  });
}

// Render helper — wraps EditableProfilePage in the /p/:slug route so
// useParams() actually resolves the URL slug (renderWithProviders'
// MemoryRouter has no route table by default).
function renderAt(url: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/p/:slug" element={<EditableProfilePage />} />
    </Routes>,
    {
      withAuth: true,
      withRouter: true,
      initialEntries: [url],
    },
  );
}

describe("EditableProfilePage — visitor paths (non-owner MUST NOT see edit chrome)", () => {
  beforeEach(() => {
    // Prevent the AuthProvider's silent refresh from causing an unhandled
    // request in the anonymous-visitor case (MSW is in error mode).
    server.use(
      http.post("/auth/refresh_token", () => new HttpResponse(null, { status: 401 })),
    );
  });

  it("anonymous visitor: renders PublicDashboard, NO edit chrome", async () => {
    server.use(...baseHandlers({ slug: "someone-else" }));

    renderAt("/p/someone-else");

    // The public shell renders the username in a testid'd h1.
    expect(await screen.findByTestId("public-username")).toHaveTextContent(
      "someone-else",
    );
    // Owner-only chrome MUST be absent.
    expect(screen.queryByTestId("profile-mode-toggle")).not.toBeInTheDocument();
    expect(screen.queryByTestId("profile-editor")).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("profile-editor-save-chrome"),
    ).not.toBeInTheDocument();
  });

  it("logged-in but viewing FOREIGN profile: renders PublicDashboard, NO edit chrome", async () => {
    // Owner "panda" viewing /p/someone-else — slug mismatch, must render
    // read-only.
    login("panda");
    server.use(
      ...baseHandlers({
        slug: "someone-else",
        ownerUsername: "panda",
        ownerSlug: "panda", // caller's slug is 'panda', not 'someone-else'
      }),
    );

    renderAt("/p/someone-else");

    expect(await screen.findByTestId("public-username")).toHaveTextContent(
      "someone-else",
    );
    // Wait for the profile fetch to resolve before asserting absence —
    // otherwise a stale null profile could hide a false negative.
    await waitFor(() => {
      expect(
        screen.queryByTestId("profile-mode-toggle"),
      ).not.toBeInTheDocument();
    });
    expect(screen.queryByTestId("profile-editor")).not.toBeInTheDocument();
    expect(
      screen.queryByTestId("profile-editor-save-chrome"),
    ).not.toBeInTheDocument();
  });
});

describe("EditableProfilePage — owner path (edit chrome + editor visible)", () => {
  beforeEach(() => {
    server.use(
      http.post("/auth/refresh_token", () => new HttpResponse(null, { status: 401 })),
    );
  });

  it("owner viewing own profile (slug match): mode toggle visible; flipping to Edit shows Save/Discard chrome", async () => {
    login("panda");
    server.use(
      ...baseHandlers({
        slug: "panda", // URL slug
        ownerUsername: "panda",
        ownerSlug: "panda", // caller's slug === URL slug ⇒ owner
      }),
    );

    renderAt("/p/panda");

    // Preview mode is the default -> public shell renders + toggle
    // appears once the profile fetch settles.
    const toggle = await screen.findByTestId("profile-mode-toggle");
    expect(toggle).toBeInTheDocument();

    // Public shell is currently rendered (preview mode).
    expect(screen.getByTestId("public-username")).toHaveTextContent("panda");
    expect(
      screen.queryByTestId("profile-editor-save-chrome"),
    ).not.toBeInTheDocument();

    // Flip to edit mode.
    const user = userEvent.setup();
    await user.click(screen.getByTestId("profile-mode-edit"));

    // Editor + Save chrome appears; public shell hero is hidden.
    expect(await screen.findByTestId("profile-editor")).toBeInTheDocument();
    expect(
      screen.getByTestId("profile-editor-save-chrome"),
    ).toBeInTheDocument();
    // Save button is initially disabled (draft matches server).
    const saveBtn = screen.getByTestId("profile-editor-save");
    expect(saveBtn).toBeDisabled();
  });

  it("owner match via username fallback (profile row has no slug set) still triggers edit mode", async () => {
    // Caller logged in as 'panda', but their /profile row hasn't been
    // enabled yet (slug === null). The fallback ownership rule matches
    // username against URL slug — this catches the "I just registered
    // and my slug row hasn't been created" onboarding case.
    login("panda");
    server.use(
      ...baseHandlers({
        slug: "panda",
        ownerUsername: "panda",
        ownerSlug: null, // profile.slug is null in the response
      }),
    );

    renderAt("/p/panda");

    expect(await screen.findByTestId("profile-mode-toggle")).toBeInTheDocument();
  });
});
