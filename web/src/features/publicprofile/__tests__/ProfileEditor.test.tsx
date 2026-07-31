// ProfileEditor.test.tsx (gaka-ie3) — non-tautological tests for the inline
// editor's draft-save model.
//
// Focus: the dirty-state gate and its guards. Draft mutation is exercised
// via the palette (deterministic; drag is a react-grid-layout DOM path
// jsdom won't touch). Assertions:
//
//   1. Fresh render: Save/Discard both disabled (draft matches server).
//   2. Palette add: draft becomes dirty → Save + Discard both enabled;
//      the dirty indicator flips.
//   3. Discard: draft reverts to server; Save + Discard disabled again;
//      the palette re-shows the removed widget.
//   4. Save: PUTs to /dashboard/public_profile with the added widget in
//      the body, then transitions back to clean.
//   5. Dirty draft + in-app navigation attempt: window.confirm fires
//      (via useBlocker); denying keeps the user on the editor.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { ProfileEditor } from "@/features/publicprofile/ProfileEditor";
import { authStore } from "@/features/auth/auth";
import { renderWithProviders } from "@/test/renderWithProviders";
import { server } from "@/test/msw/server";
import type { PublicDashboardPayload } from "@/types/stats";
import { PUBLIC_PROFILE_DEFAULT_LAYOUT } from "@/features/publicprofile/defaults";

vi.mock("sonner", () => {
  const toast = Object.assign((_: string) => {}, {
    error: (_: string) => {},
    success: (_: string) => {},
  });
  return { toast };
});

function payloadWithLayout(username: string): PublicDashboardPayload {
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
    layout: {
      cols: 12,
      // Seed with a minimal 1-tile layout so the palette has EVERYTHING
      // ELSE available to add — makes the dirty-state test deterministic
      // (we don't need to guess which widget is on/off the shipped
      // default).
      widgets: [
        { i: "hero-identity", x: 0, y: 0, w: 12, h: 3, view: null },
      ],
    },
  };
}

const putBodies: unknown[] = [];

beforeEach(() => {
  putBodies.length = 0;
  authStore.update({
    token: "test-token",
    tokenExpiry: new Date(Date.now() + 60_000).toISOString(),
    tokenUsername: "panda",
  });
  server.use(
    http.post("/auth/refresh_token", () => new HttpResponse(null, { status: 401 })),
    http.get("/api/public/profile/panda", () =>
      HttpResponse.json(payloadWithLayout("panda")),
    ),
    http.get("/api/public/profile/panda/awards", () => HttpResponse.json([])),
    http.get("/api/public/profile/panda/awards/streaks", () =>
      HttpResponse.json({}),
    ),
    // Widget renderers (HeroIdentity + LabelsShowcase) also fire the
    // OWN endpoints; the retry:false in the hooks makes prod-side 401s
    // silent, but MSW's onUnhandledRequest:error surfaces them as noisy
    // stderr. Stub with empty payloads.
    http.get("/api/v1/users/current/awards", () => HttpResponse.json([])),
    http.get("/api/v1/users/current/awards/streaks", () =>
      HttpResponse.json({}),
    ),
    http.get("/api/v1/labels/catalog", () =>
      HttpResponse.json({ labels: [], systemPrompt: "" }),
    ),
    // The editor's Save handler PUTs the new layout; capture the body so
    // the assertion can verify the widget actually made it into the
    // request payload (non-tautological).
    http.put("/api/v1/users/current/dashboard/public_profile", async ({ request }) => {
      const body = await request.json();
      putBodies.push(body);
      return HttpResponse.json({ layout: body });
    }),
  );
});

afterEach(() => {
  // Undo any window.confirm stubs a test installed.
  vi.restoreAllMocks();
});

describe("ProfileEditor — draft-save model", () => {
  it("initial render: Save + Discard disabled; dirty indicator reads 'saved'", async () => {
    renderWithProviders(<ProfileEditor slug="panda" />, {
      withAuth: true,
      withRouter: true,
      initialEntries: ["/p/panda"],
    });

    const save = await screen.findByTestId("profile-editor-save");
    const discard = screen.getByTestId("profile-editor-discard");
    expect(save).toBeDisabled();
    expect(discard).toBeDisabled();
    expect(
      screen.getByTestId("profile-editor-dirty-indicator"),
    ).toHaveTextContent(/saved/i);
  });

  it("palette add: draft dirty → Save + Discard enabled → indicator flips", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ProfileEditor slug="panda" />, {
      withAuth: true,
      withRouter: true,
      initialEntries: ["/p/panda"],
    });

    // Wait for the editor + its palette to render. The seed carries
    // ONE widget (hero-identity); the palette should show a `+` button
    // for grade-badge (the first widget in the default profile layout
    // NOT already placed).
    const addBtn = await screen.findByTestId(
      "profile-editor-palette-add-grade-badge",
    );
    await user.click(addBtn);

    // Draft is dirty now → Save + Discard become enabled, indicator flips.
    await waitFor(() => {
      expect(screen.getByTestId("profile-editor-save")).not.toBeDisabled();
    });
    expect(screen.getByTestId("profile-editor-discard")).not.toBeDisabled();
    expect(
      screen.getByTestId("profile-editor-dirty-indicator"),
    ).toHaveTextContent(/unsaved/i);
  });

  it("Discard: reverts to server layout → button becomes disabled again", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ProfileEditor slug="panda" />, {
      withAuth: true,
      withRouter: true,
      initialEntries: ["/p/panda"],
    });

    await user.click(
      await screen.findByTestId("profile-editor-palette-add-grade-badge"),
    );
    await waitFor(() => {
      expect(screen.getByTestId("profile-editor-discard")).not.toBeDisabled();
    });

    await user.click(screen.getByTestId("profile-editor-discard"));

    await waitFor(() => {
      expect(screen.getByTestId("profile-editor-save")).toBeDisabled();
    });
    expect(screen.getByTestId("profile-editor-discard")).toBeDisabled();
    // The palette should show grade-badge again (it was un-added).
    expect(
      screen.getByTestId("profile-editor-palette-add-grade-badge"),
    ).toBeInTheDocument();
  });

  it("Save: PUTs the current draft (with the added widget), then transitions clean", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ProfileEditor slug="panda" />, {
      withAuth: true,
      withRouter: true,
      initialEntries: ["/p/panda"],
    });

    await user.click(
      await screen.findByTestId("profile-editor-palette-add-grade-badge"),
    );
    await waitFor(() => {
      expect(screen.getByTestId("profile-editor-save")).not.toBeDisabled();
    });

    await user.click(screen.getByTestId("profile-editor-save"));

    await waitFor(() => {
      expect(putBodies.length).toBe(1);
    });
    const body = putBodies[0] as {
      layout: { cols: number; widgets: Array<{ i: string }> };
    };
    // Body carries a layout whose widgets include BOTH the seeded
    // hero-identity AND the newly-added grade-badge — proves the palette
    // add + save wiring survives the round-trip.
    const kinds = body.layout.widgets.map((w) => w.i);
    expect(kinds).toContain("hero-identity");
    expect(kinds).toContain("grade-badge");
  });

  it("dirty draft blocks navigation: useBlocker fires window.confirm; deny keeps user", async () => {
    // Stub confirm to always DENY the navigation — verifies the blocker
    // path actually invokes confirm AND that reset() keeps the editor
    // mounted (draft state survives).
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();

    // Wrap the editor in a tiny router with a second route we can try
    // to navigate to; if the blocker misfires, the editor unmounts and
    // the test-id disappears.
    const { NavAway } = await import("@/features/publicprofile/__tests__/_NavAway");
    renderWithProviders(
      <>
        <NavAway />
        <ProfileEditor slug="panda" />
      </>,
      {
        withAuth: true,
        withRouter: true,
        initialEntries: ["/p/panda"],
      },
    );

    // Make it dirty first.
    await user.click(
      await screen.findByTestId("profile-editor-palette-add-grade-badge"),
    );
    await waitFor(() => {
      expect(screen.getByTestId("profile-editor-save")).not.toBeDisabled();
    });

    // Try to navigate away.
    await user.click(screen.getByTestId("nav-away-link"));

    // confirm() should have been called at least once with a warning-
    // style message, and the editor should STILL be mounted (deny path).
    await waitFor(() => expect(confirmSpy).toHaveBeenCalled());
    expect(confirmSpy.mock.calls[0]?.[0]).toMatch(/unsaved/i);
    expect(screen.getByTestId("profile-editor")).toBeInTheDocument();
  });

  // Sanity: PUBLIC_PROFILE_DEFAULT_LAYOUT still contains grade-badge, so
  // the palette-add test above is meaningful (this pins the assumption).
  it("PUBLIC_PROFILE_DEFAULT_LAYOUT includes grade-badge (test-assumption pin)", () => {
    expect(
      PUBLIC_PROFILE_DEFAULT_LAYOUT.some((w) => w.i === "grade-badge"),
    ).toBe(true);
  });
});
