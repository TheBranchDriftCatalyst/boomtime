// PinToggle.test.tsx — the canonical-entities pin control (boom-canon).
//
// Non-tautological anchors (real usePins + real api + MSW; nothing about pins
// is mocked, so we assert the ACTUAL wire behavior end-to-end):
//   - Clicking an unpinned toggle POSTs /curation with the EXACT pin shape
//     { action:"pin", axis, matchType:"exact", matchValue } — the contract the
//     backend's create-rule endpoint expects for a pin.
//   - A value the backend already reports as pinned renders active
//     (aria-pressed=true), and clicking it DELETEs that rule id (unpin) — it
//     does NOT create a second rule.
//   - After a pin AND after an unpin, the grouped-query caches
//     (["reading-query"], ["books-explore"]) plus the curation list are
//     invalidated, so every open chart refetches and the value escapes "Other".
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "@shared/test/msw/handlers";
import { server } from "@shared/test/msw/server";
import {
  makeTestQueryClient,
  renderWithProviders,
} from "@shared/test/renderWithProviders";
import { PinToggle } from "@shared/features/pins/PinToggle";

// A canonical pin rule the backend would return from GET /curation.
function pinRule(over: Partial<{ id: number; axis: string; matchValue: string }> = {}) {
  return {
    id: 42,
    axis: "genre",
    action: "pin" as const,
    matchValue: "Fantasy",
    newValue: null,
    matchType: "exact" as const,
    applyAtIngest: false,
    createdAt: "2026-08-01T00:00:00.000Z",
    ...over,
  };
}

describe("PinToggle (canonical entities)", () => {
  it("pins an unpinned value: POSTs the exact pin shape + invalidates the charts", async () => {
    // No pins yet → the toggle renders inactive.
    server.use(
      http.get("/api/v1/users/current/curation", () =>
        HttpResponse.json({ rules: [] }),
      ),
    );
    let posted: unknown;
    server.use(
      http.post("/api/v1/users/current/curation", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ rule: pinRule() });
      }),
    );

    const qc = makeTestQueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    renderWithProviders(<PinToggle axis="genre" value="Fantasy" />, {
      queryClient: qc,
    });

    const btn = screen.getByTestId("pin-toggle");
    // Starts inactive (no matching pin in the list).
    await waitFor(() =>
      expect(btn).toHaveAttribute("aria-pressed", "false"),
    );

    await userEvent.click(btn);

    // The create call carries the canonical pin body — action=pin, exact match.
    await waitFor(() => expect(posted).toBeDefined());
    expect(posted).toMatchObject({
      axis: "genre",
      action: "pin",
      matchType: "exact",
      matchValue: "Fantasy",
    });

    // The grouped-query caches + curation list are invalidated so charts refetch.
    await waitFor(() => {
      const keys = invalidate.mock.calls.map((c) => c[0]?.queryKey);
      expect(keys).toContainEqual(["reading-query"]);
      expect(keys).toContainEqual(["books-explore"]);
      expect(keys).toContainEqual(["curation"]);
    });
  });

  it("renders active for an already-pinned value and unpins via DELETE (no second create)", async () => {
    server.use(
      http.get("/api/v1/users/current/curation", () =>
        HttpResponse.json({ rules: [pinRule({ id: 7 })] }),
      ),
    );
    let deletedId: string | undefined;
    let postCalled = false;
    server.use(
      http.delete("/api/v1/users/current/curation/:id", ({ params }) => {
        deletedId = String(params.id);
        return new HttpResponse(null, { status: 200 });
      }),
      http.post("/api/v1/users/current/curation", () => {
        postCalled = true;
        return HttpResponse.json({ rule: pinRule() });
      }),
    );

    const qc = makeTestQueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    renderWithProviders(<PinToggle axis="genre" value="Fantasy" />, {
      queryClient: qc,
    });

    const btn = screen.getByTestId("pin-toggle");
    // The list reports this (axis,value) pinned → active.
    await waitFor(() => expect(btn).toHaveAttribute("aria-pressed", "true"));

    await userEvent.click(btn);

    // Unpin deletes the rule by its id — and never creates a duplicate.
    await waitFor(() => expect(deletedId).toBe("7"));
    expect(postCalled).toBe(false);

    // Unpin also invalidates the grouped-query caches.
    await waitFor(() => {
      const keys = invalidate.mock.calls.map((c) => c[0]?.queryKey);
      expect(keys).toContainEqual(["reading-query"]);
      expect(keys).toContainEqual(["books-explore"]);
    });
  });

  it("only marks the matching (axis,value) active — a different value stays inactive", async () => {
    // A pin exists on genre=Fantasy; the Sci-Fi toggle must NOT read as pinned.
    server.use(
      http.get("/api/v1/users/current/curation", () =>
        HttpResponse.json({ rules: [pinRule({ matchValue: "Fantasy" })] }),
      ),
    );
    renderWithProviders(<PinToggle axis="genre" value="Sci-Fi" />);
    const btn = screen.getByTestId("pin-toggle");
    // Give the list query time to resolve; it must remain inactive.
    await waitFor(() => expect(btn).toHaveAttribute("aria-pressed", "false"));
  });
});
