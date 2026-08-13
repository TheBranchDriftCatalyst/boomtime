import { MemoryRouter } from "react-router";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { curationLayer } from "@/features/curation/explorer/curationLayer";
import { server } from "@/test/msw/server";
import { http, HttpResponse } from "@/test/msw/handlers";
import type { GroupAction } from "@/features/explorer/types";
import type { GroupNode } from "@/features/explorer/explorerModel";

// The layer is exercised entirely on its own — no <GroupableExplorer>, no
// heartbeats config — proving it is a self-contained abstraction that renders
// into the neutral GroupDecoration shape for any (axis, value) group node.

function node(axis: string, value: string | null): GroupNode {
  return {
    kind: "group",
    id: `g:${axis}=${value}`,
    axis,
    value,
    stats: { count: 1 },
    depth: 0,
    path: [],
    drillable: true,
  };
}

function makeQC() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
}

function seed() {
  server.use(
    http.get("/api/v1/users/current/curation", () =>
      HttpResponse.json({
        rules: [
          // project "catalyst" is suppressed (hidden).
          { id: 1, axis: "project", action: "hide", matchValue: "catalyst", matchType: "exact", newValue: null },
          // project "gaka" is renamed to "GAKA".
          { id: 2, axis: "project", action: "rename", matchValue: "gaka", matchType: "exact", newValue: "GAKA" },
        ],
      }),
    ),
    http.get("/api/v1/users/current/spaces", () =>
      HttpResponse.json({
        spaces: [{ id: 1, name: "Work", position: 0, ruleCount: 1 }],
      }),
    ),
    http.get("/api/v1/users/current/spaces/1", () =>
      HttpResponse.json({
        id: 1,
        name: "Work",
        position: 0,
        rules: [{ id: 10, axis: "project", matchValue: "work-proj", matchType: "exact" }],
      }),
    ),
  );
}

// A host that renders one node's decoration slots via the layer's hook.
function Decorated({ useLayer, n }: { useLayer: () => GroupAction; n: GroupNode }) {
  const decorate = useLayer();
  const d = decorate(n, []);
  return (
    <div>
      <span data-testid="dimmed">{String(!!d.dimmed)}</span>
      <div>{d.badges}</div>
      <div>{d.actions}</div>
    </div>
  );
}

function renderNode(useLayer: () => GroupAction, n: GroupNode) {
  const qc = makeQC();
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Decorated useLayer={useLayer} n={n} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("curationLayer (isolated)", () => {
  it("decorates a suppressed group with dimming + a Hidden badge + unsuppress action", async () => {
    seed();
    const useLayer = curationLayer();
    renderNode(useLayer, node("project", "catalyst"));

    expect(await screen.findByText("Hidden")).toBeInTheDocument();
    expect(await screen.findByTitle('Unsuppress "catalyst"')).toBeInTheDocument();
    expect(screen.getByTestId("dimmed")).toHaveTextContent("true");
  });

  it("shows a remap badge + rename action for a renamed group", async () => {
    seed();
    const useLayer = curationLayer();
    renderNode(useLayer, node("project", "gaka"));

    expect(await screen.findByText("→ GAKA")).toBeInTheDocument();
    expect(screen.getByTitle('Rename Project "gaka"')).toBeInTheDocument();
    expect(screen.getByTestId("dimmed")).toHaveTextContent("false");
  });

  it("badges Space membership and offers add-to-Space", async () => {
    seed();
    const useLayer = curationLayer();
    renderNode(useLayer, node("project", "work-proj"));

    // Membership badge (the Space name link).
    expect(await screen.findByText("Work")).toBeInTheDocument();
    expect(
      screen.getByTitle('Add Project "work-proj" to a Space'),
    ).toBeInTheDocument();
  });

  it("offers no curation on a non-curatable axis (day)", async () => {
    seed();
    const useLayer = curationLayer();
    renderNode(useLayer, node("day", "2026-01-01"));

    // Let the curation/space queries settle, then assert nothing curation-y.
    expect(await screen.findByTestId("dimmed")).toHaveTextContent("false");
    expect(screen.queryByText("Hidden")).not.toBeInTheDocument();
    expect(screen.queryByTitle(/Rename/)).not.toBeInTheDocument();
    expect(screen.queryByTitle(/to a Space/)).not.toBeInTheDocument();
  });

  it("drags in nothing when every feature is opted out", async () => {
    seed();
    const useLayer = curationLayer({ suppress: false, rename: false, spaces: false });
    renderNode(useLayer, node("project", "catalyst"));

    expect(await screen.findByTestId("dimmed")).toHaveTextContent("false");
    expect(screen.queryByText("Hidden")).not.toBeInTheDocument();
    expect(screen.queryByTitle(/Unsuppress/)).not.toBeInTheDocument();
    expect(screen.queryByTitle(/Rename/)).not.toBeInTheDocument();
    expect(screen.queryByTitle(/to a Space/)).not.toBeInTheDocument();
  });
});
