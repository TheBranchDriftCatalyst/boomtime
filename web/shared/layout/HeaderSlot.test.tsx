// HeaderSlot.test.tsx — the hardening guard for TALOS-6y60.
//
// The blank-Admin-content incident came from the header slot's WRITE bouncing
// back into the writer: one context held {node, setNode}, so its value changed
// identity on every write and every useHeaderSlot caller (a consumer of that
// context) re-rendered as a result of its own write. A writer whose node has an
// unstable identity then loops until React's update-depth bail blanks the
// routed subtree.
//
// The fix splits the node and the setter into separate contexts, so the setter
// context's value — the useState setter — never changes identity. The invariant
// asserted here is exactly that: A WRITE MUST NOT RE-RENDER THE WRITER. That is
// what removes the feedback edge, and without an edge back into the writer no
// node, however unstable, can close a cycle.
//
// Asserted by RENDER COUNT with a *stable* node, deliberately. An assertion
// that renders the loop itself HANGS the runner instead of failing it (the loop
// blocks the event loop synchronously — confirmed at 120s and 400s timeouts),
// so every test in this class counts renders on inputs that cannot loop either
// way. Same reasoning as shared/admin/AdminSectionPage.test.tsx.
import { useMemo, useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  HeaderSlotProvider,
  useHeaderSlot,
  useHeaderSlotNode,
} from "./HeaderSlot";

/** Mirrors a real page: memoized node, hoisted into the header on mount. */
function Writer({
  onRender,
  label = "tabs",
}: {
  onRender: () => void;
  label?: string;
}) {
  onRender();
  const node = useMemo(() => <span data-testid="slot">{label}</span>, [label]);
  useHeaderSlot(node);
  return <div data-testid="page">page body</div>;
}

/** Mirrors HeaderBar: reads the slot, renders it when a page has set one. */
function Reader({ onRender }: { onRender: () => void }) {
  onRender();
  const slot = useHeaderSlotNode();
  return <header>{slot ?? <span data-testid="default">default</span>}</header>;
}

describe("useHeaderSlot", () => {
  it("does not re-render the writer when its own write lands", () => {
    let writerRenders = 0;

    render(
      <HeaderSlotProvider>
        <Writer onRender={() => writerRenders++} />
      </HeaderSlotProvider>,
    );

    // Exactly one render: mount, then the mount effect's setNode, which must
    // NOT come back around. Before the read/write split this was 2 — the writer
    // subscribed to the same context it wrote, so the write re-rendered it.
    // That extra render is the loop's edge: with a node whose identity is
    // rebuilt each render it re-runs the effect, writes again, and never
    // settles.
    expect(writerRenders).toBe(1);
  });

  it("re-renders the writer only for its own props/state, never for the write", () => {
    let writerRenders = 0;
    let readerRenders = 0;

    function Harness() {
      const [label, setLabel] = useState("first");
      return (
        <HeaderSlotProvider>
          <Reader onRender={() => readerRenders++} />
          <Writer onRender={() => writerRenders++} label={label} />
          <button onClick={() => setLabel("second")}>change</button>
        </HeaderSlotProvider>
      );
    }

    render(<Harness />);
    expect(screen.getByTestId("slot")).toHaveTextContent("first");

    fireEvent.click(screen.getByRole("button", { name: "change" }));

    // Two writer renders total: the mount and the label change. Neither of the
    // two writes (initial node, changed node) adds one. Unsplit, each write
    // added a render on top — 4 — which is the same edge that becomes an
    // unbounded loop the moment the node identity is unstable.
    expect(writerRenders).toBe(2);
    // The reader is the component that SHOULD re-render on a header change:
    // mount, the initial write, the label re-render, the second write.
    expect(readerRenders).toBe(4);
    expect(screen.getByTestId("slot")).toHaveTextContent("second");
  });

  it("clears the slot when the writer unmounts", () => {
    function Harness({ show }: { show: boolean }) {
      return (
        <HeaderSlotProvider>
          <Reader onRender={() => {}} />
          {show ? <Writer onRender={() => {}} /> : null}
        </HeaderSlotProvider>
      );
    }

    const { rerender } = render(<Harness show />);
    expect(screen.getByTestId("slot")).toBeInTheDocument();

    rerender(<Harness show={false} />);
    expect(screen.queryByTestId("slot")).not.toBeInTheDocument();
    expect(screen.getByTestId("default")).toBeInTheDocument();
  });

  it("no-ops outside a provider instead of throwing", () => {
    expect(() => render(<Writer onRender={() => {}} />)).not.toThrow();
    expect(screen.getByTestId("page")).toBeInTheDocument();
  });
});
