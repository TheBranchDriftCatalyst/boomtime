import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useSpaceMutations } from "@/features/spaces/useSpaces";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";

// The scoped-dashboard keys every Space/rule change must invalidate, plus the
// space list (and, when known, the one space's detail).
const DEPENDENT_KEYS = [
  ["stats"],
  ["project-stats"],
  ["projects"],
  ["leaderboards"],
  ["timeline"],
  ["punchcard"],
  ["sessions"],
  ["momentum"],
  ["cross-project-files"],
];

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

function makeClient() {
  const qc = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  });
  return { qc, spy: vi.spyOn(qc, "invalidateQueries") };
}

describe("useSpaceMutations invalidation (P0)", () => {
  it("addRule invalidates ['spaces'] + ['space', id] + the scoped dashboard keys", async () => {
    server.use(
      http.post("/api/v1/users/current/spaces/:id/rules", () =>
        HttpResponse.json({
          rule: {
            id: 7,
            axis: "project",
            matchValue: "^catalyst",
            matchType: "regex",
          },
        }),
      ),
    );
    const { qc, spy } = makeClient();
    const { result } = renderHook(() => useSpaceMutations(), {
      wrapper: wrapper(qc),
    });

    result.current.addRule.mutate({
      id: 3,
      body: { axis: "project", matchValue: "^catalyst", matchType: "regex" },
    });
    await waitFor(() =>
      expect(result.current.addRule.isSuccess).toBe(true),
    );

    const keys = spy.mock.calls.map((c) => c[0]?.queryKey);
    expect(keys).toContainEqual(["spaces"]);
    expect(keys).toContainEqual(["space", "3"]);
    for (const key of DEPENDENT_KEYS) {
      expect(keys).toContainEqual(key);
    }
  });

  it("create invalidates ['spaces'] + the new space's detail + scoped keys", async () => {
    server.use(
      http.post("/api/v1/users/current/spaces", () =>
        HttpResponse.json({ space: { id: 42, name: "Work", position: 0 } }),
      ),
    );
    const { qc, spy } = makeClient();
    const { result } = renderHook(() => useSpaceMutations(), {
      wrapper: wrapper(qc),
    });

    result.current.create.mutate("Work");
    await waitFor(() => expect(result.current.create.isSuccess).toBe(true));

    const keys = spy.mock.calls.map((c) => c[0]?.queryKey);
    expect(keys).toContainEqual(["spaces"]);
    expect(keys).toContainEqual(["space", "42"]);
    for (const key of DEPENDENT_KEYS) {
      expect(keys).toContainEqual(key);
    }
  });

  it("delete invalidates ['spaces'] + the deleted space + scoped keys", async () => {
    server.use(
      http.delete("/api/v1/users/current/spaces/:id", () =>
        new HttpResponse(null, { status: 200 }),
      ),
    );
    const { qc, spy } = makeClient();
    const { result } = renderHook(() => useSpaceMutations(), {
      wrapper: wrapper(qc),
    });

    result.current.remove.mutate(9);
    await waitFor(() => expect(result.current.remove.isSuccess).toBe(true));

    const keys = spy.mock.calls.map((c) => c[0]?.queryKey);
    expect(keys).toContainEqual(["spaces"]);
    expect(keys).toContainEqual(["space", "9"]);
  });
});
