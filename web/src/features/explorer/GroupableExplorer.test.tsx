import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { GroupableExplorer } from "@/features/explorer/GroupableExplorer";
import type { DomainConfig, GroupPage, LeafResult } from "@/features/explorer/types";

interface Row {
  id: string;
  name: string;
}

const ALL_ROWS: Row[] = [
  { id: "r1", name: "row-1" },
  { id: "r2", name: "row-2" },
  { id: "r3", name: "row-3" },
];

// A fake TreeSource: axis "a" yields two root groups, axis "b" one; the leaf
// pages a fixed 3-row set at pageSize 2 so pagination exercises two pages.
function makeSource() {
  const fetchGroup = vi.fn(
    async (
      _path: unknown,
      axis: string,
    ): Promise<GroupPage> => {
      if (axis === "b") {
        return {
          groups: [{ value: "b1", stats: { count: 5, sum: 50 } }],
          truncated: false,
        };
      }
      return {
        groups: [
          { value: "a1", stats: { count: 3, sum: 30 } },
          { value: "a2", stats: { count: 1, sum: 10 } },
        ],
        truncated: false,
      };
    },
  );
  const fetchLeaf = vi.fn(
    async (
      _path: unknown,
      page: number,
      pageSize: number,
    ): Promise<LeafResult<Row>> => {
      const start = (page - 1) * pageSize;
      return {
        rows: ALL_ROWS.slice(start, start + pageSize),
        total: ALL_ROWS.length,
        page,
        limit: pageSize,
      };
    },
  );
  return { fetchGroup, fetchLeaf };
}

function makeConfig(
  source: ReturnType<typeof makeSource>,
  overrides: Partial<DomainConfig<Row>> = {},
): DomainConfig<Row> {
  return {
    axes: [
      { id: "a", label: "A" },
      { id: "b", label: "B" },
    ],
    defaultGroupBy: ["a"],
    columns: [
      {
        id: "name",
        header: "Name",
        get: (r) => r.name,
        render: (r) => r.name,
        defaultVisible: true,
      },
    ],
    rollups: [{ id: "sum", label: "Sum", format: (n) => `${n}s` }],
    source,
    rowKey: (r) => r.id,
    leafPageSize: 2,
    labels: { leafGroup: "Rows" },
    ...overrides,
  };
}

// Controlled host so groupBy changes flow through like the real page.
function Harness({
  config,
  initialGroupBy,
}: {
  config: DomainConfig<Row>;
  initialGroupBy: string[];
}) {
  const [groupBy, setGroupBy] = useState(initialGroupBy);
  return (
    <div>
      <button onClick={() => setGroupBy(["b"])}>set-b</button>
      <GroupableExplorer
        config={config}
        groupBy={groupBy}
        onGroupByChange={setGroupBy}
        resetKey="k"
      />
    </div>
  );
}

describe("GroupableExplorer", () => {
  it("expands a terminal group directly to its rows (no intermediate label)", async () => {
    const user = userEvent.setup();
    const source = makeSource();
    render(<Harness config={makeConfig(source)} initialGroupBy={["a"]} />);

    // Root groups + count badge + formatted rollup.
    await screen.findByText("a1");
    expect(screen.getByText("a2")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument(); // count badge
    expect(screen.getByText("30s")).toBeInTheDocument(); // rollup format

    // Expand a1 (the deepest axis) -> its leaf rows render DIRECTLY: no
    // intermediate "Rows" entity-label node to expand a second time.
    await user.click(screen.getByText("a1"));
    await screen.findByText("row-1");
    expect(screen.getByText("row-2")).toBeInTheDocument();
    expect(screen.queryByText("row-3")).not.toBeInTheDocument();
    // The redundant per-group leaf-group label is gone in the grouped view.
    expect(screen.queryByText("Rows")).not.toBeInTheDocument();
    // The pager now sits inline under the terminal group.
    expect(screen.getByText("Page 1 / 2")).toBeInTheDocument();
    // Leaf rows were fetched for a1's drill path on the single expand.
    expect(source.fetchLeaf).toHaveBeenCalledWith(
      [{ dim: "a", value: "a1" }],
      1,
      2,
    );

    // Next page -> the third row, driven by the inline pager.
    await user.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByText("row-3");
    expect(screen.getByText("Page 2 / 2")).toBeInTheDocument();
  });

  it("resets and reloads the root when groupBy changes", async () => {
    const user = userEvent.setup();
    const source = makeSource();
    render(<Harness config={makeConfig(source)} initialGroupBy={["a"]} />);

    await screen.findByText("a1");

    // Switch the group-by axis to "b": the root reloads with b-axis groups.
    await user.click(screen.getByRole("button", { name: "set-b" }));
    await screen.findByText("b1");
    expect(screen.queryByText("a1")).not.toBeInTheDocument();
    expect(source.fetchGroup).toHaveBeenCalledWith([], "b", ["sum"]);
  });

  it("renders a domain group decoration on each node", async () => {
    const source = makeSource();
    const config = makeConfig(source, {
      useGroupDecorator: () => (node) => ({
        badges: <span data-testid={`badge-${node.value}`}>★</span>,
      }),
    });
    render(<Harness config={config} initialGroupBy={["a"]} />);

    expect(await screen.findByTestId("badge-a1")).toBeInTheDocument();
    expect(screen.getByTestId("badge-a2")).toBeInTheDocument();
  });

  it("renders leaf rows directly when there are zero group axes", async () => {
    const source = makeSource();
    // No addAxisHint => the flat "Table" view: rows show without drilling.
    render(<Harness config={makeConfig(source)} initialGroupBy={[]} />);

    await screen.findByText("row-1");
    expect(screen.getByText("row-2")).toBeInTheDocument();
    // page 1 of the fixed set (limit 2) — the third row is not on this page.
    expect(screen.queryByText("row-3")).not.toBeInTheDocument();
    // No grouping happened.
    expect(source.fetchGroup).not.toHaveBeenCalled();
    expect(source.fetchLeaf).toHaveBeenCalledWith([], 1, 2);
  });

  it("paginates the flat root leaf view across pages when there are zero axes", async () => {
    const user = userEvent.setup();
    const source = makeSource();
    render(<Harness config={makeConfig(source)} initialGroupBy={[]} />);

    // The flat root auto-expands its synthetic leaf group: page 1 (2 of 3
    // rows) shows immediately, driven by the same fetchLeaf([], 1, 2) call the
    // drilled leaves use — the third row is on page 2.
    await screen.findByText("row-1");
    expect(screen.getByText("row-2")).toBeInTheDocument();
    expect(screen.queryByText("row-3")).not.toBeInTheDocument();
    expect(source.fetchLeaf).toHaveBeenCalledWith([], 1, 2);
    expect(source.fetchGroup).not.toHaveBeenCalled();

    // total (3) drives the page count via the same leaf pager the drilled
    // leaves render, and the Next affordance is available at the flat root.
    expect(screen.getByText("Page 1 / 2")).toBeInTheDocument();
    const next = screen.getByRole("button", { name: "Next" });
    expect(next).toBeInTheDocument();

    // Next advances the flat root to page 2 (the third row) — no drilling.
    await user.click(next);
    await screen.findByText("row-3");
    expect(screen.getByText("Page 2 / 2")).toBeInTheDocument();
    expect(source.fetchLeaf).toHaveBeenCalledWith([], 2, 2);
    expect(source.fetchGroup).not.toHaveBeenCalled();
  });

  it("does not render the flat view when an addAxisHint is configured", async () => {
    const source = makeSource();
    const config = makeConfig(source, {
      labels: { leafGroup: "Rows", addAxisHint: "Add an axis to explore." },
    });
    render(<Harness config={config} initialGroupBy={[]} />);

    await screen.findByText("Add an axis to explore.");
    expect(source.fetchLeaf).not.toHaveBeenCalled();
    expect(screen.queryByText("row-1")).not.toBeInTheDocument();
  });
});
