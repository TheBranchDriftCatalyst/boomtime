import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useCurationMutations } from "@/features/curation/useCuration";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

// The exact set the hook must invalidate after any rule change.
const EXPECTED_KEYS = [
  ["curation"],
  ["stats"],
  ["project-stats"],
  ["projects"],
  ["leaderboards"],
  ["timeline"],
  ["punchcard"],
  ["sessions"],
  ["momentum"],
  ["cross-project-files"],
  ["hb-explore-group"],
  ["hb-explore-list"],
  ["derived-status"],
  ["axis-values"],
];

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe("useCurationMutations invalidation (P0)", () => {
  it("invalidates exactly ['curation'] + the DEPENDENT_KEYS on add", async () => {
    server.use(
      http.post("/api/v1/users/current/curation", () =>
        HttpResponse.json({ rule: { id: 1 } }),
      ),
    );
    const qc = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    });
    const spy = vi.spyOn(qc, "invalidateQueries");

    const { result } = renderHook(() => useCurationMutations(), {
      wrapper: wrapper(qc),
    });

    result.current.add.mutate({
      axis: "project",
      action: "hide",
      matchValue: "x",
    });

    await waitFor(() => expect(result.current.add.isSuccess).toBe(true));

    const invalidatedKeys = spy.mock.calls.map((c) => c[0]?.queryKey);
    for (const key of EXPECTED_KEYS) {
      expect(invalidatedKeys).toContainEqual(key);
    }
    // Exactly these, no extras.
    expect(spy).toHaveBeenCalledTimes(EXPECTED_KEYS.length);
  });

  it("also invalidates the same keys on remove", async () => {
    server.use(
      http.delete("/api/v1/users/current/curation/:id", () =>
        new HttpResponse(null, { status: 200 }),
      ),
    );
    const qc = new QueryClient({
      defaultOptions: { mutations: { retry: false } },
    });
    const spy = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => useCurationMutations(), {
      wrapper: wrapper(qc),
    });

    result.current.remove.mutate(1);
    await waitFor(() => expect(result.current.remove.isSuccess).toBe(true));

    // Removing a rule also invalidates the affected-heartbeats previews.
    const invalidatedKeys = spy.mock.calls.map((c) => c[0]?.queryKey);
    for (const key of [...EXPECTED_KEYS, ["curation-affected"]]) {
      expect(invalidatedKeys).toContainEqual(key);
    }
    expect(spy).toHaveBeenCalledTimes(EXPECTED_KEYS.length + 1);
  });
});
