import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useSpaceMembership } from "@/features/heartbeats/useSpaceMembership";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { GroupNode } from "@/features/heartbeats/explorerModel";
import type { HeartbeatAxis } from "@/types/api";

function wrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

function makeQC() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

// Minimal GroupNode for the axis/value fields the hook reads.
function node(axis: HeartbeatAxis, value: string | null): GroupNode {
  return {
    kind: "group",
    id: `g:${axis}=${value}`,
    axis,
    value,
    count: 1,
    seconds: 0,
    firstSeen: "",
    lastSeen: "",
    depth: 0,
    childFilters: {},
    drillable: false,
  };
}

// Space list + per-space detail fixtures. "Work" has an exact project rule and
// a regex one; "Personal" has an exact language rule.
function seedSpaces() {
  server.use(
    http.get("/api/v1/users/current/spaces", () =>
      HttpResponse.json({
        spaces: [
          { id: 1, name: "Work", position: 0, ruleCount: 2 },
          { id: 2, name: "Personal", position: 1, ruleCount: 1 },
        ],
      }),
    ),
    http.get("/api/v1/users/current/spaces/1", () =>
      HttpResponse.json({
        id: 1,
        name: "Work",
        position: 0,
        rules: [
          { id: 10, axis: "project", matchValue: "catalyst", matchType: "exact" },
          { id: 11, axis: "project", matchValue: "^exp-", matchType: "regex" },
        ],
      }),
    ),
    http.get("/api/v1/users/current/spaces/2", () =>
      HttpResponse.json({
        id: 2,
        name: "Personal",
        position: 1,
        rules: [
          { id: 20, axis: "language", matchValue: "Go", matchType: "exact" },
        ],
      }),
    ),
  );
}

describe("useSpaceMembership", () => {
  it("badges a value via EXACT and REGEX membership rules", async () => {
    seedSpaces();
    const qc = makeQC();
    const { result } = renderHook(() => useSpaceMembership(), {
      wrapper: wrapper(qc),
    });

    await waitFor(() =>
      expect(result.current.getSpacesFor(node("project", "catalyst"))).toHaveLength(1),
    );

    // Exact project rule -> Work membership.
    expect(result.current.getSpacesFor(node("project", "catalyst"))).toEqual([
      { spaceId: 1, spaceName: "Work", ruleId: 10 },
    ]);
    // A value matched by the regex rule (^exp-) IS badged, via that rule.
    expect(result.current.getSpacesFor(node("project", "exp-thing"))).toEqual([
      { spaceId: 1, spaceName: "Work", ruleId: 11 },
    ]);
    // A value the regex does not match is not badged.
    expect(result.current.getSpacesFor(node("project", "not-exp"))).toEqual([]);
    // Exact language rule -> Personal.
    expect(result.current.getSpacesFor(node("language", "Go"))).toEqual([
      { spaceId: 2, spaceName: "Personal", ruleId: 20 },
    ]);
    // A value in no space.
    expect(result.current.getSpacesFor(node("project", "other"))).toEqual([]);
  });

  it("shows one badge per Space when a value matches both an exact and a regex rule", async () => {
    // "Work" gets an extra regex rule (^catalyst) that also matches "catalyst",
    // which already has the exact rule (id 10). The badge must not duplicate.
    server.use(
      http.get("/api/v1/users/current/spaces", () =>
        HttpResponse.json({
          spaces: [{ id: 1, name: "Work", position: 0, ruleCount: 3 }],
        }),
      ),
      http.get("/api/v1/users/current/spaces/1", () =>
        HttpResponse.json({
          id: 1,
          name: "Work",
          position: 0,
          rules: [
            { id: 10, axis: "project", matchValue: "catalyst", matchType: "exact" },
            { id: 12, axis: "project", matchValue: "^catalyst", matchType: "regex" },
            // An unparseable pattern must be skipped, not throw.
            { id: 13, axis: "project", matchValue: "(", matchType: "regex" },
          ],
        }),
      ),
    );
    const qc = makeQC();
    const { result } = renderHook(() => useSpaceMembership(), {
      wrapper: wrapper(qc),
    });
    await waitFor(() => expect(result.current.spaceOptions).toHaveLength(1));

    // Matched by both the exact and the regex rule -> exactly one badge.
    expect(result.current.getSpacesFor(node("project", "catalyst"))).toEqual([
      { spaceId: 1, spaceName: "Work", ruleId: 10 },
    ]);
  });

  it("gates add-to-Space to concrete values on curatable axes", async () => {
    seedSpaces();
    const qc = makeQC();
    const { result } = renderHook(() => useSpaceMembership(), {
      wrapper: wrapper(qc),
    });
    await waitFor(() => expect(result.current.spaceOptions).toHaveLength(2));

    expect(result.current.canAddToSpace(node("project", "catalyst"))).toBe(true);
    // Null value (the "(none)" group) can't be added.
    expect(result.current.canAddToSpace(node("project", null))).toBe(false);
    // A non-curatable axis (day) can't be added.
    expect(result.current.canAddToSpace(node("day", "2026-01-01"))).toBe(false);
  });

  it("adds an EXACT membership rule to the chosen Space", async () => {
    seedSpaces();
    let posted: { url: string; body: unknown } | null = null;
    server.use(
      http.post(
        "/api/v1/users/current/spaces/:id/rules",
        async ({ request, params }) => {
          posted = { url: String(params.id), body: await request.json() };
          return HttpResponse.json({
            rule: { id: 99, axis: "project", matchValue: "newproj", matchType: "exact" },
          });
        },
      ),
    );

    const qc = makeQC();
    const { result } = renderHook(() => useSpaceMembership(), {
      wrapper: wrapper(qc),
    });
    await waitFor(() => expect(result.current.spaceOptions).toHaveLength(2));

    result.current.addToSpace(node("project", "newproj"), 1, "Work");

    await waitFor(() => expect(posted).not.toBeNull());
    expect(posted!.url).toBe("1");
    expect(posted!.body).toEqual({
      axis: "project",
      matchValue: "newproj",
      matchType: "exact",
    });
  });
});
