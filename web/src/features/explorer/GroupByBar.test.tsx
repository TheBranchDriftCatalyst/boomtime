import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { GroupByBar } from "@/features/explorer/GroupByBar";
import type { Axis } from "@/features/explorer/types";

const AXES: Axis[] = [
  { id: "source", label: "Source" },
  { id: "status", label: "Status" },
  { id: "author", label: "Author", section: "Meta" },
];

// A minimal DataTransfer stand-in: jsdom does not implement one, so back the
// getData/setData pair with a plain store shared across a drag sequence.
function makeDataTransfer() {
  const store: Record<string, string> = {};
  return {
    effectAllowed: "",
    dropEffect: "",
    setData: (type: string, val: string) => {
      store[type] = val;
    },
    getData: (type: string) => store[type] ?? "",
  };
}

function dragChip(from: number, to: number) {
  const dataTransfer = makeDataTransfer();
  fireEvent.dragStart(screen.getByTestId(`groupby-chip-${from}`), {
    dataTransfer,
  });
  fireEvent.dragOver(screen.getByTestId(`groupby-chip-${to}`), { dataTransfer });
  fireEvent.drop(screen.getByTestId(`groupby-chip-${to}`), { dataTransfer });
}

describe("GroupByBar", () => {
  it("renders one numbered chip per active axis in order", () => {
    render(
      <GroupByBar axes={AXES} groupBy={["source", "status"]} onChange={vi.fn()} />,
    );
    expect(screen.getByText("Source")).toBeInTheDocument();
    expect(screen.getByText("Status")).toBeInTheDocument();
    // Row-number badges reflect order.
    expect(screen.getByTestId("groupby-chip-0")).toHaveTextContent("1");
    expect(screen.getByTestId("groupby-chip-0")).toHaveTextContent("Source");
    expect(screen.getByTestId("groupby-chip-1")).toHaveTextContent("2");
    expect(screen.getByTestId("groupby-chip-1")).toHaveTextContent("Status");
  });

  it("drags chip 2 to position 1 and calls onChange with the reordered array", () => {
    const onChange = vi.fn();
    render(
      <GroupByBar
        axes={AXES}
        groupBy={["source", "status"]}
        onChange={onChange}
      />,
    );
    // Drag "Status" (index 1) onto "Source" (index 0).
    dragChip(1, 0);
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith(["status", "source"]);
  });

  it("drags chip 1 to the last position across three axes", () => {
    const onChange = vi.fn();
    render(
      <GroupByBar
        axes={AXES}
        groupBy={["source", "status", "author"]}
        onChange={onChange}
      />,
    );
    // Drag index 0 onto index 2: splice-move keeps the middle's relative order.
    dragChip(0, 2);
    expect(onChange).toHaveBeenCalledWith(["status", "author", "source"]);
  });

  it("dropping a chip on itself is a no-op", () => {
    const onChange = vi.fn();
    render(
      <GroupByBar
        axes={AXES}
        groupBy={["source", "status"]}
        onChange={onChange}
      />,
    );
    dragChip(1, 1);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("still supports the ‹ › arrow reordering as a fallback", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <GroupByBar
        axes={AXES}
        groupBy={["source", "status"]}
        onChange={onChange}
      />,
    );
    // Move "Source" (index 0) right → swaps with "Status".
    await user.click(screen.getAllByTitle("Move right")[0]);
    expect(onChange).toHaveBeenCalledWith(["status", "source"]);
  });

  it("removes an axis via the ✕ button", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <GroupByBar
        axes={AXES}
        groupBy={["source", "status"]}
        onChange={onChange}
      />,
    );
    await user.click(screen.getAllByTitle("Remove")[0]);
    expect(onChange).toHaveBeenCalledWith(["status"]);
  });

  it("adds an available axis from the picker", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <GroupByBar axes={AXES} groupBy={["source"]} onChange={onChange} />,
    );
    await user.click(screen.getByRole("button", { name: /add axis/i }));
    await user.click(screen.getByRole("button", { name: "Status" }));
    expect(onChange).toHaveBeenCalledWith(["source", "status"]);
  });
});
