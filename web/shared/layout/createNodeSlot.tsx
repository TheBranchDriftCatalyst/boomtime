// createNodeSlot — the factory behind the shell's "portal in state" slots.
//
// A slot is one shared ReactNode held in context: some region of the shell
// READS it and renders it; a page WRITES it on mount and clears it on unmount.
// It is how a deeply-routed page hands chrome (a tab strip, an action row) to a
// region it doesn't own, without prop-drilling through the shell or reaching
// for a real DOM portal.
//
// This factory exists because the pattern now has more than one instance
// (HeaderSlot, PageActionsSlot) and ONE detail of it is genuinely dangerous to
// re-derive by hand:
//
// ── THE READ/WRITE SPLIT IS LOAD-BEARING ────────────────────────────────────
// The node and the setter MUST live in separate contexts. With a single
// context holding `{node, setNode}`, the context value's identity changes
// whenever the node does — so a WRITER (which must useContext to reach the
// setter) re-renders every time it writes. Feed such a writer a node derived
// fresh each render and that closes a cycle:
//
//   fresh node identity → effect re-runs → setNode → context value changes
//   → writer re-renders → fresh node identity → …
//
// React caps that with "Maximum update depth exceeded" and BAILS ON THE
// SUBTREE, so the routed content renders EMPTY while the slot's own node stays
// painted. It reads as "chrome fine, content blank", no error boundary fires
// (the update-depth bail is a console.error, not a throw), and it blanked every
// /app/admin/* route in production once already. See HeaderSlot.tsx's
// TALOS-6y60 note for the full post-mortem.
//
// Split apart, the setter context's value is the useState setter itself, stable
// for the provider's entire life. A writer never re-renders from its own write,
// and the worst an unstable node can do is re-render the reader once.
import { createContext, useContext, useEffect, useState } from "react";
import type { ReactNode } from "react";

type SetSlotNode = (node: ReactNode) => void;

export interface NodeSlot {
  /** Wrap the region that contains BOTH the reader and the writer. */
  Provider: (props: { children: ReactNode }) => ReactNode;
  /**
   * Render `node` into the slot for as long as the caller is mounted; clears on
   * unmount. Prefer a memoized `node` — it is the effect's dependency, so one
   * rebuilt every render re-runs the effect and re-renders the reader. That is
   * waste, not a hang (see the read/write split above). No-ops outside a
   * Provider, so a page is safe to call it in any mounting context.
   */
  useSet: (node: ReactNode) => void;
  /**
   * Read the current node (null when nobody set one). Subscribing here is what
   * makes a component re-render on a slot change — do NOT call it in a
   * component that also calls useSet.
   */
  useNode: () => ReactNode;
}

/**
 * Build an independent slot. `displayName` only labels the contexts for
 * devtools; each call produces a fresh, isolated pair of contexts.
 */
export function createNodeSlot(displayName: string): NodeSlot {
  const NodeContext = createContext<ReactNode>(null);
  const SetterContext = createContext<SetSlotNode | null>(null);
  NodeContext.displayName = `${displayName}.Node`;
  SetterContext.displayName = `${displayName}.Setter`;

  function Provider({ children }: { children: ReactNode }) {
    const [node, setNode] = useState<ReactNode>(null);
    // `setNode` is a useState setter — React guarantees a stable identity for
    // the provider's life, so the setter context needs no memoization and its
    // consumers never re-render on a node change.
    return (
      <SetterContext.Provider value={setNode}>
        <NodeContext.Provider value={node}>{children}</NodeContext.Provider>
      </SetterContext.Provider>
    );
  }
  Provider.displayName = `${displayName}Provider`;

  function useSet(node: ReactNode): void {
    const setNode = useContext(SetterContext);
    useEffect(() => {
      if (!setNode) return;
      setNode(node);
      return () => setNode(null);
    }, [node, setNode]);
  }

  function useNode(): ReactNode {
    return useContext(NodeContext);
  }

  return { Provider, useSet, useNode };
}
